package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"

	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
)

const (
	// Duration after which to flush powerusage stats to disk.
	FLUSH_POWER_USAGE = time.Minute
)

var (
	configFile  = flag.String("conf", "/etc/powercycle.yaml", "YAML file with device configuration.")
	delay       = flag.Int("delay", 0, "Any value > 0 overrides the default duration (in sec) between turning the port off and on.")
	listDev     = flag.Bool("list_devices", false, "List the available devices and exit.")
	powerCycle  = flag.Bool("power_cycle", true, "Powercycle the given devices.")
	powerOutput = flag.String("power_output", "", "Continously poll power usage and write it to the given file. Press ^C to exit.")
	sampleRate  = flag.Duration("power_sample_rate", 2*time.Second, "Time delay between capturing power usage.")
)

// DeviceGroup describes a set of devices that can all be
// controlled together. Any switch or power strip needs to
// implement this interface.
type DeviceGroup interface {
	// DeviceIDs returns a list of strings that uniquely identify
	// the devices that can be controlled through this group.
	DeviceIDs() []string

	// PowerCycle turns the device off for a reasonable amount of time
	// (i.e. 10 seconds) and then turns it back on. If delayOverride
	// is larger than zero it overrides the default delay between
	// turning the port off and on again.
	PowerCycle(devID string, delayOverride time.Duration) error

	// PowerUsage returns the power usage of all devices in the group
	// at a specific time.
	PowerUsage() (*GroupPowerUsage, error)
}

// GroupPowerUsage captures power usage of a set of devices at
// a specific time stamp.
type GroupPowerUsage struct {
	// Time stamp when the usage sample was taken.
	TS time.Time
	// Map[deviceID]PowerStat capturs power usage for the devices in the group.
	Stats map[string]*PowerStat
}

// PowerStat captures the current, voltage and wattage.
type PowerStat struct {
	Ampere float32 // current in mA
	Volt   float32 // voltage in V
	Watt   float32 // wattage in W (redundant ~Ampere * Volt / 1000)
}

func main() {
	common.Init()
	devGroup, err := DeviceGroupFromYamlFile(*configFile)
	if err != nil {
		sklog.Fatalf("Unable to parse config file.  Got error: %s", err)
	}

	if *listDev {
		listDevices(devGroup, 0)
	} else if *powerOutput != "" {
		if *sampleRate <= 0 {
			sklog.Fatal("Non-positive sample rate provided.")
		}
		tailPower(devGroup, *powerOutput, *sampleRate)
	}

	// No device id given.
	args := flag.Args()
	if len(args) == 0 {
		sklog.Errorf("No device id given to power cycle.")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Check if the device ids are valid.
	validDeviceIds := devGroup.DeviceIDs()
	for _, arg := range args {
		if !util.In(arg, validDeviceIds) {
			sklog.Errorf("Invalid device ID.")
			listDevices(devGroup, 1)
		}
	}

	for _, deviceID := range args {
		if err := devGroup.PowerCycle(deviceID, time.Duration(*delay)*time.Second); err != nil {
			sklog.Fatalf("Unable to power cycle device %s. Got error: %s", deviceID, err)
		}

		sklog.Infof("Power cycle successful. All done.")
		sklog.Flush()
	}
}

// listDevices prints out the devices it know about. This implies that
// the devices have been contacted and passed a ping test.
func listDevices(devGroup DeviceGroup, exitCode int) {
	fmt.Fprintf(os.Stderr, "Valid device IDs are:\n\n")
	for _, id := range devGroup.DeviceIDs() {
		fmt.Fprintf(os.Stderr, "    %s\n", id)
	}
	os.Exit(exitCode)
}

// tailPower continually polls the power usage and writes the values in
// a CSV file.
func tailPower(devGroup DeviceGroup, outputPath string, sampleRate time.Duration) {
	f, err := os.Create(outputPath)
	if err != nil {
		sklog.Fatalf("Unable to open file '%s': Go error: %s", outputPath, err)
	}
	writer := csv.NewWriter(f)

	// Catch Ctrl-C to flush the file.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		sklog.Infof("Closing cvs file.")
		sklog.Flush()
		writer.Flush()
		util.LogErr(f.Close())
		os.Exit(0)
	}()

	var ids []string = nil
	lastFlush := time.Now()
	for range time.Tick(sampleRate) {
		// get power stats
		powerStats, err := devGroup.PowerUsage()
		if err != nil {
			sklog.Errorf("Error getting power stats: %s", err)
			continue
		}

		if ids == nil {
			ids = make([]string, 0, len(powerStats.Stats))
			for id := range powerStats.Stats {
				ids = append(ids, id)
			}
			sort.Strings(ids)

			recs := make([]string, 0, len(ids)*3+1)
			recs = append(recs, "time")
			for _, id := range ids {
				recs = append(recs, id+"-A")
				recs = append(recs, id+"-V")
				recs = append(recs, id+"-W")
			}
			if err := writer.Write(recs); err != nil {
				sklog.Errorf("Error writing CSV records: %s", err)
			}
		}

		recs := make([]string, 0, len(ids)*3+1)
		recs = append(recs, powerStats.TS.String())
		var stats *PowerStat
		var ok bool
		for _, id := range ids {
			stats, ok = powerStats.Stats[id]
			if !ok {
				sklog.Errorf("Unable to find expected id: %s", id)
				break
			}
			recs = append(recs, fmt.Sprintf("%5.3f", stats.Ampere))
			recs = append(recs, fmt.Sprintf("%5.3f", stats.Volt))
			recs = append(recs, fmt.Sprintf("%5.3f", stats.Watt))
		}
		if ok {
			if err := writer.Write(recs); err != nil {
				sklog.Errorf("Error writing CSV records: %s", err)
			}

			if time.Now().Sub(lastFlush) >= FLUSH_POWER_USAGE {
				lastFlush = time.Now()
				writer.Flush()
			}
		}
	}
}
