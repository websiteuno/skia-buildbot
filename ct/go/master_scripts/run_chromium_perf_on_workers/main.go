// run_chromium_perf_on_workers is an application that runs the specified telemetry
// benchmark on all CT workers and uploads the results to Google Storage. The
// requester is emailed when the task is done.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/golang/glog"
	"skia.googlesource.com/buildbot.git/ct/go/util"
	"skia.googlesource.com/buildbot.git/go/common"
)

var (
	emails                    = flag.String("emails", "", "The comma separated email addresses to notify when the task is picked up and completes.")
	gaeTaskID                 = flag.Int("gae_task_id", -1, "The key of the App Engine task. This task will be updated when the task is completed.")
	pagesetType               = flag.String("pageset_type", "", "The type of pagesets to use. Eg: 10k, Mobile10k, All.")
	benchmarkName             = flag.String("benchmark_name", "", "The telemetry benchmark to run on the workers.")
	benchmarkExtraArgs        = flag.String("benchmark_extra_args", "", "The extra arguments that are passed to the specified benchmark.")
	browserExtraArgsNoPatch   = flag.String("browser_extra_args_nopatch", "", "The extra arguments that are passed to the browser while running the benchmark for the nopatch case.")
	browserExtraArgsWithPatch = flag.String("browser_extra_args_withpatch", "", "The extra arguments that are passed to the browser while running the benchmark for the withpatch case.")
	repeatBenchmark           = flag.Int("repeat_benchmark", 1, "The number of times the benchmark should be repeated. For skpicture_printer benchmark this value is always 1.")
	targetPlatform            = flag.String("target_platform", util.PLATFORM_ANDROID, "The platform the benchmark will run on (Android / Linux).")
	runID                     = flag.String("run_id", "", "The unique run id (typically requester + timestamp).")
	varianceThreshold         = flag.Int("variance_threshold", 0, "The variance threshold to use when comparing the resultant CSV files.")
	discardOutliers           = flag.Int("discard_outliers", 0, "The percentage of outliers to discard when comparing the result CSV files.")

	taskCompletedSuccessfully = false

	htmlOutputLink      = ""
	skiaPatchLink       = ""
	blinkPatchLink      = ""
	chromiumPatchLink   = ""
	noPatchOutputLink   = ""
	withPatchOutputLink = ""
)

func sendEmail() {
	// Send completion email.
	emailsArr := util.ParseEmails(*emails)
	if len(emailsArr) == 0 {
		glog.Error("At least one email address must be specified")
		return
	}
	emailSubject := fmt.Sprintf("Cluster telemetry chromium perf task has completed (%s)", *runID)
	if !taskCompletedSuccessfully {
		emailSubject += " with failures"
	}
	// TODO(rmistry): Add a link to the master logs here and maybe a table with
	// links to logs of the 100 slaves.
	bodyTemplate := `
	The chromium perf %s benchmark task on %s pageset has completed.<br/>
	The HTML output with differences between the base run and the patch run is <a href='%s'>here</a>.<br/>
	The patch(es) you specified are here:
	<a href='%s'>chromium</a>/<a href='%s'>blink</a>/<a href='%s'>skia</a>
	<br/><br/>
	You can schedule more runs <a href='%s'>here</a>.
	<br/><br/>
	Thanks!
	`
	emailBody := fmt.Sprintf(bodyTemplate, *benchmarkName, *pagesetType, htmlOutputLink, chromiumPatchLink, blinkPatchLink, skiaPatchLink, util.ChromiumPerfTasksWebapp)
	if err := util.SendEmail(emailsArr, emailSubject, emailBody); err != nil {
		glog.Errorf("Error while sending email: %s", err)
		return
	}
}

func updateWebappTask() {
	// TODO(rmistry): Add ability to update the webapp without providing links.
	extraData := map[string]string{
		"skia_patch_link":              skiaPatchLink,
		"blink_patch_link":             blinkPatchLink,
		"chromium_patch_link":          chromiumPatchLink,
		"telemetry_nopatch_log_link":   noPatchOutputLink,
		"telemetry_withpatch_log_link": withPatchOutputLink,
		"html_output_link":             htmlOutputLink,
		"build_log_link":               util.MASTER_LOGSERVER_LINK,
	}
	if err := util.UpdateWebappTask(*gaeTaskID, util.UpdateChromiumPerfTasksWebapp, extraData); err != nil {
		glog.Errorf("Error while updating webapp task: %s", err)
		return
	}
}

func main() {
	common.Init()
	// Ensure webapp is updated and email is sent even if task fails.
	defer updateWebappTask()
	defer sendEmail()
	// Cleanup dirs after run completes.
	defer os.RemoveAll(filepath.Join(util.StorageDir, util.ChromiumPerfRunsDir))
	defer os.RemoveAll(filepath.Join(util.StorageDir, util.BenchmarkRunsDir))
	// Finish with glog flush and how long the task took.
	defer util.TimeTrack(time.Now(), "Running chromium perf task on workers")
	defer glog.Flush()

	if *pagesetType == "" {
		glog.Error("Must specify --pageset_type")
		return
	}
	if *benchmarkName == "" {
		glog.Error("Must specify --benchmark_name")
		return
	}
	if *runID == "" {
		glog.Error("Must specify --run_id")
		return
	}

	// Create the two required chromium builds (with patch and without the patch).
	chromiumHash, skiaHash, err := util.CreateChromiumBuild(*runID, *targetPlatform, "", "", true)
	if err != nil {
		glog.Errorf("Could not create chromium build: %s", err)
		return
	}

	// Instantiate GsUtil object.
	gs, err := util.NewGsUtil(nil)
	if err != nil {
		glog.Errorf("Could not instantiate gsutil object: %s", err)
		return
	}
	remoteOutputDir := filepath.Join(util.ChromiumPerfRunsDir, *runID)

	// Copy the patches to Google Storage.
	skiaPatchName := *runID + ".skia.patch"
	blinkPatchName := *runID + ".blink.patch"
	chromiumPatchName := *runID + ".chromium.patch"
	for _, patchName := range []string{skiaPatchName, blinkPatchName, chromiumPatchName} {
		if err := gs.UploadFile(patchName, os.TempDir(), remoteOutputDir); err != nil {
			glog.Errorf("Could not upload %s to %s: %s", patchName, remoteOutputDir, err)
			return
		}
	}
	skiaPatchLink = util.GS_HTTP_LINK + filepath.Join(util.GS_BUCKET_NAME, remoteOutputDir, skiaPatchName)
	blinkPatchLink = util.GS_HTTP_LINK + filepath.Join(util.GS_BUCKET_NAME, remoteOutputDir, blinkPatchName)
	chromiumPatchLink = util.GS_HTTP_LINK + filepath.Join(util.GS_BUCKET_NAME, remoteOutputDir, chromiumPatchName)

	// Reboot all workers to start from a clean slate.
	util.RebootWorkers()

	// Call run_benchmark_on_slaves for withpatch.
	runIDWithPatch := *runID + "-withpatch"
	chromiumBuildWithPatch := fmt.Sprintf("try-%s-%s-%s", chromiumHash, skiaHash, runIDWithPatch)
	if err := runBenchmarkOnWorkers(chromiumBuildWithPatch, runIDWithPatch); err != nil {
		glog.Errorf("Error while running benchmark on workers for runID %s: %s", runIDWithPatch, err)
		return
	}

	// Reboot all workers to start from a clean slate.
	util.RebootWorkers()

	// Call run_benchmark_on_slaves for nopatch.
	runIDNoPatch := *runID + "-nopatch"
	chromiumBuildNoPatch := fmt.Sprintf("try-%s-%s-%s", chromiumHash, skiaHash, runIDNoPatch)
	if err := runBenchmarkOnWorkers(chromiumBuildNoPatch, runIDNoPatch); err != nil {
		glog.Errorf("Error while running benchmark on workers for runID %s: %s", runIDNoPatch, err)
		return
	}

	// Compare the resultant CSV files using csv_comparer.py
	noPatchCSVPath := filepath.Join(util.StorageDir, util.BenchmarkRunsDir, runIDNoPatch, runIDNoPatch+".output")
	withPatchCSVPath := filepath.Join(util.StorageDir, util.BenchmarkRunsDir, runIDWithPatch, runIDWithPatch+".output")
	htmlOutputDir := filepath.Join(util.StorageDir, util.ChromiumPerfRunsDir, *runID, "html")
	os.MkdirAll(htmlOutputDir, 0700)
	htmlRemoteDir := filepath.Join(remoteOutputDir, "html")
	htmlOutputLinkBase := util.GS_HTTP_LINK + filepath.Join(util.GS_BUCKET_NAME, htmlRemoteDir) + "/"
	htmlOutputLink = htmlOutputLinkBase + "index.html"
	noPatchOutputLink = util.GS_HTTP_LINK + filepath.Join(util.GS_BUCKET_NAME, util.BenchmarkRunsDir, runIDNoPatch, "consolidated_outputs", runIDNoPatch+".output")
	withPatchOutputLink = util.GS_HTTP_LINK + filepath.Join(util.GS_BUCKET_NAME, util.BenchmarkRunsDir, runIDWithPatch, "consolidated_outputs", runIDWithPatch+".output")
	// Construct path to the csv_comparer python script.
	_, currentFile, _, _ := runtime.Caller(0)
	pathToPyFiles := filepath.Join(
		filepath.Dir((filepath.Dir(filepath.Dir(filepath.Dir(currentFile))))),
		"py")
	pathToCsvComparer := filepath.Join(pathToPyFiles, "csv_comparer.py")
	args := []string{
		pathToCsvComparer,
		"--csv_file1=" + noPatchCSVPath,
		"--csv_file2=" + withPatchCSVPath,
		"--output_html=" + htmlOutputDir,
		"--variance_threshold=" + strconv.Itoa(*varianceThreshold),
		"--discard_outliers=" + strconv.Itoa(*discardOutliers),
		"--absolute_url=" + htmlOutputLinkBase,
		"--requester_email=" + *emails,
		"--skia_patch_link=" + skiaPatchLink,
		"--blink_patch_link=" + blinkPatchLink,
		"--chromium_patch_link=" + chromiumPatchLink,
		"--raw_csv_nopatch=" + noPatchOutputLink,
		"--raw_csv_withpatch=" + withPatchOutputLink,
		"--num_repeated=" + strconv.Itoa(*repeatBenchmark),
		"--target_platform=" + *targetPlatform,
		"--browser_args_nopatch=" + *browserExtraArgsNoPatch,
		"--browser_args_withpatch=" + *browserExtraArgsWithPatch,
		"--pageset_type=" + *pagesetType,
	}
	if err := util.ExecuteCmd("python", args, []string{}, 2*time.Hour, nil, nil); err != nil {
		glog.Errorf("Error running csv_comparer.py: %s", err)
		return
	}

	// Copy the HTML files to Google Storage.
	if err := gs.UploadDir(htmlOutputDir, htmlRemoteDir); err != nil {
		glog.Errorf("Could not upload %s to %s: %s", htmlOutputDir, htmlRemoteDir, err)
		return
	}

	taskCompletedSuccessfully = true
}

func runBenchmarkOnWorkers(chromiumBuild, id string) error {
	runBenchmarkWithPatchCmdTemplate := "DISPLAY=:0 run_benchmark_on_workers --log_dir={{.LogDir}} " +
		"--pageset_type={{.PagesetType}} --chromium_build={{.ChromiumBuild}} --run_id={{.RunID}} " +
		"--benchmark_name={{.BenchmarkName}} --benchmark_extra_args=\"{{.BenchmarkExtraArgs}}\" " +
		"--browser_extra_args=\"{{.BrowserExtraArgs}}\" --repeat_benchmark={{.RepeatBenchmark}} " +
		"--target_platform={{.TargetPlatform}} --tryserver_run=true;"
	runBenchmarkWithPatchTemplateParsed := template.Must(template.New("run_benchmark_withpatch_cmd").Parse(runBenchmarkWithPatchCmdTemplate))
	benchmarkWithPatchCmdBytes := new(bytes.Buffer)
	runBenchmarkWithPatchTemplateParsed.Execute(benchmarkWithPatchCmdBytes, struct {
		LogDir             string
		PagesetType        string
		ChromiumBuild      string
		RunID              string
		BenchmarkName      string
		BenchmarkExtraArgs string
		BrowserExtraArgs   string
		RepeatBenchmark    int
		TargetPlatform     string
	}{
		LogDir:             util.GLogDir,
		PagesetType:        *pagesetType,
		ChromiumBuild:      chromiumBuild,
		RunID:              id,
		BenchmarkName:      *benchmarkName,
		BenchmarkExtraArgs: *benchmarkExtraArgs,
		BrowserExtraArgs:   *browserExtraArgsWithPatch,
		RepeatBenchmark:    *repeatBenchmark,
		TargetPlatform:     *targetPlatform,
	})
	cmd := []string{
		fmt.Sprintf("cd %s;", util.CtTreeDir),
		"git pull;",
		"make worker_scripts;",
		// The main command that runs run_benchmark_on_workers.
		benchmarkWithPatchCmdBytes.String(),
	}
	if _, err := util.SSH(strings.Join(cmd, " "), util.Slaves, 6*time.Hour); err != nil {
		return fmt.Errorf("Error while running cmd %s: %s", cmd, err)
	}
	return nil
}
