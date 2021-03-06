#!/bin/bash
#
# Create and setup a Swarming instance.
#
# Copyright 2014 Google Inc. All Rights Reserved.
# Author: rmistry@google.com (Ravi Mistry)

source vm_config.sh
source vm_setup_utils.sh

# Set OS specific GCE variables.
if [ "$VM_INSTANCE_OS" == "Linux" ]; then
  SKIA_BOT_IMAGE_NAME=$SKIA_BOT_LINUX_IMAGE_NAME
  REQUIRED_FILES_FOR_BOTS=${REQUIRED_FILES_FOR_LINUX_BOTS[@]}
  DISK_ARGS="--boot-disk-size=20GB"
  if [ "$VM_IS_SWARMINGBOT" = 1 ]; then
    SKIA_BOT_IMAGE_NAME=$SKIA_SWARMING_IMAGE_NAME
    SKIA_BOT_MACHINE_TYPE="n1-standard-16"
  fi
  if [ "$VM_IS_CTBOT" = 1 ]; then
    SKIA_BOT_MACHINE_TYPE="n1-highmem-2"
    if [ "$VM_IS_CTBUILDER" = 1 ]; then
      # Builders need to be beefier.
      SKIA_BOT_MACHINE_TYPE="custom-32-70400"
    fi
  fi
elif [ "$VM_INSTANCE_OS" == "Windows" ]; then
  SKIA_BOT_IMAGE_NAME=$SKIA_BOT_WIN_IMAGE_NAME
  ORIG_SYSPREP_SCRIPT="../../scripts/win_setup.ps1"
  MODIFIED_SYSPREP_SCRIPT="/tmp/win_setup.ps1"
  # Set chrome-bot's password in win_setup.ps1
  cp $ORIG_SYSPREP_SCRIPT $MODIFIED_SYSPREP_SCRIPT
  WIN_CHROME_BOT_PWD=$(echo $(cat /tmp/win-chrome-bot.txt) | sed -e 's/[\/&]/\\&/g')
  sed -i "s/CHROME_BOT_PASSWORD/${WIN_CHROME_BOT_PWD}/g" $MODIFIED_SYSPREP_SCRIPT
  python ../../scripts/insert_file.py $MODIFIED_SYSPREP_SCRIPT $MODIFIED_SYSPREP_SCRIPT

  # Fix line endings in $MODIFIED_SYSPREP_SCRIPT. 'todos' is in the 'tofrodos'
  # package on Ubuntu.
  todos $MODIFIED_SYSPREP_SCRIPT

  ORIG_STARTUP_SCRIPT="../../scripts/win_startup.ps1"
  MODIFIED_STARTUP_SCRIPT="/tmp/win_startup.ps1"
  cp $ORIG_STARTUP_SCRIPT $MODIFIED_STARTUP_SCRIPT
  sed -i "s/CHROME_BOT_PASSWORD/${WIN_CHROME_BOT_PWD}/g" $MODIFIED_STARTUP_SCRIPT
  todos $MODIFIED_STARTUP_SCRIPT

  ORIG_SCHTASK_SCRIPT="../../scripts/chromebot-schtask.ps1"
  MODIFIED_SCHTASK_SCRIPT="/tmp/chromebot-schtask.ps1"
  cp $ORIG_SCHTASK_SCRIPT $MODIFIED_SCHTASK_SCRIPT
  todos $MODIFIED_SCHTASK_SCRIPT

  METADATA_ARGS="--metadata=gce-initial-windows-user=chrome-bot"
  METADATA_ARGS+=" --metadata-from-file="
  METADATA_ARGS+="gce-initial-windows-password=/tmp/win-chrome-bot.txt"
  METADATA_ARGS+=",sysprep-oobe-script-ps1=$MODIFIED_SYSPREP_SCRIPT"
  METADATA_ARGS+=",windows-startup-script-ps1=$MODIFIED_STARTUP_SCRIPT"
  METADATA_ARGS+=",chromebot-schtask-ps1=$MODIFIED_SCHTASK_SCRIPT"
  DISK_ARGS="--boot-disk-size=${VM_PERSISTENT_DISK_SIZE_GB}GB \
             --boot-disk-type=pd-ssd"
  REQUIRED_FILES_FOR_BOTS=${REQUIRED_FILES_FOR_WIN_BOTS[@]}
else
  echo "$VM_INSTANCE_OS is not recognized!"
  exit 1
fi

# Create all requested instances.
ALL_INSTANCE_NAMES=""
for MACHINE_IP in $(seq $VM_BOT_COUNT_START $VM_BOT_COUNT_END); do
  INSTANCE_NAME=${VM_BOT_NAME}-`printf "%03d" ${MACHINE_IP}`
  ALL_INSTANCE_NAMES+=" ${INSTANCE_NAME}"
  EXTERNAL_IP_ADDRESS=${IP_ADDRESS_WITHOUT_MACHINE_PART}.${MACHINE_IP}

  if [ "$VM_INSTANCE_OS" == "Linux" ]; then
    # The persistent disk of linux GCE bots is based on the bot's IP address.
    PERSISTENT_DISK_ARG="--disk=name=$PERSISTENT_DISK_NAME-`printf "%03d" ${MACHINE_IP}`"
  fi

  # As of 2017-05-05, need 'alpha' for --min-cpu-platform flag.
  gcloud ${VM_MIN_CPU_PLATFORM:+alpha} compute --project $PROJECT_ID \
    instances create ${INSTANCE_NAME} \
    --zone=$ZONE \
    --address=$EXTERNAL_IP_ADDRESS \
    --service-account=$PROJECT_USER \
    --scopes="$SCOPES" \
    --network=$SKIA_NETWORK_NAME \
    --image=$SKIA_BOT_IMAGE_NAME \
    --machine-type=$SKIA_BOT_MACHINE_TYPE \
    --boot-disk-auto-delete \
    $DISK_ARGS $METADATA_ARGS $PERSISTENT_DISK_ARG \
    ${VM_MIN_CPU_PLATFORM:+"--min-cpu-platform=${VM_MIN_CPU_PLATFORM}"}

  if [ $? -ne 0 ]; then
    echo
    echo "===== There was an error creating ${INSTANCE_NAME}. ====="
    echo
    exit 1
  fi

  if [ "$VM_INSTANCE_OS" == "Windows" ]; then
    # Specify the initial user and password again because of a bug.
    REPEAT_METADATA="gce-initial-windows-user=chrome-bot"
    REPEAT_METADATA+=",gce-initial-windows-password=$WIN_CHROME_BOT_PWD"
    gcloud compute --project $PROJECT_ID instances add-metadata \
      --metadata $REPEAT_METADATA \
      --zone $ZONE $INSTANCE_NAME
  fi
done

if [ "$VM_INSTANCE_OS" == "Windows" ]; then
  # Wait for all instances to be ready.
  for MACHINE_IP in $(seq $VM_BOT_COUNT_START $VM_BOT_COUNT_END); do
    INSTANCE_NAME=${VM_BOT_NAME}-`printf "%03d" ${MACHINE_IP}`
    DONE_TEXT="Instance setup finished. ${INSTANCE_NAME} is ready to use."
    while [ `gcloud compute instances get-serial-port-output --zone=${ZONE} ${INSTANCE_NAME} | grep -c "${DONE_TEXT}"` = 0 ]; do
      echo "Waiting 5 seconds for ${INSTANCE_NAME} to come up."
      sleep 5
    done
  done

  # Reboot all instances. This causes the startup script to run.
  gcloud compute --project $PROJECT_ID instances stop --zone $ZONE $ALL_INSTANCE_NAMES
  gcloud compute --project $PROJECT_ID instances start --zone $ZONE $ALL_INSTANCE_NAMES

  # Wait for all instances to come back from reboot and finish their startup script.
  for MACHINE_IP in $(seq $VM_BOT_COUNT_START $VM_BOT_COUNT_END); do
    INSTANCE_NAME=${VM_BOT_NAME}-`printf "%03d" ${MACHINE_IP}`
    DONE_TEXT="Finished running startup scripts."
    while [ `gcloud compute instances get-serial-port-output --zone=${ZONE} ${INSTANCE_NAME} | tail | grep -c "${DONE_TEXT}"` = 0 ]; do
      echo "Waiting 5 seconds for ${INSTANCE_NAME} to come back from reboot."
      sleep 5
    done
  done

  # The startup script enabled auto-login as chrome-bot on boot. We need to
  # reboot in order to run chrome-bot's scheduled task.
  gcloud compute --project $PROJECT_ID instances stop --zone $ZONE $ALL_INSTANCE_NAMES
  gcloud compute --project $PROJECT_ID instances start --zone $ZONE $ALL_INSTANCE_NAMES

  # Wait for all instances to come back from reboot.
  for MACHINE_IP in $(seq $VM_BOT_COUNT_START $VM_BOT_COUNT_END); do
    INSTANCE_NAME=${VM_BOT_NAME}-`printf "%03d" ${MACHINE_IP}`
    DONE_TEXT="Finished running startup scripts."
    while [ `gcloud compute instances get-serial-port-output --zone=${ZONE} ${INSTANCE_NAME} | tail | grep -c "${DONE_TEXT}"` = 0 ]; do
      echo "Waiting 5 seconds for ${INSTANCE_NAME} to come back from reboot."
      sleep 5
    done
  done

else
  echo
  echo "===== Wait for all instances to come up. ====="
  echo
  for MACHINE_IP in $(seq $VM_BOT_COUNT_START $VM_BOT_COUNT_END); do
    EXTERNAL_IP_ADDRESS=${IP_ADDRESS_WITHOUT_MACHINE_PART}.${MACHINE_IP}
    INSTANCE_NAME=${VM_BOT_NAME}-`printf "%03d" ${MACHINE_IP}`

    until nc -w 1 -z $EXTERNAL_IP_ADDRESS 22; do
      echo "Waiting for ${INSTANCE_NAME} to come up."
      sleep 2
    done
  done
fi

# Looping through all bots and setting them up.
for MACHINE_IP in $(seq $VM_BOT_COUNT_START $VM_BOT_COUNT_END); do
  INSTANCE_NAME=${VM_BOT_NAME}-`printf "%03d" ${MACHINE_IP}`

  if [ "$VM_INSTANCE_OS" == "Linux" ]; then
    FAILED=""

    install_packages

    fix_depot_tools

    setup_symlinks

    install_go

    if [ "$VM_IS_SWARMINGBOT" = 1 ]; then
      copy_files

      run_swarming_bootstrap
    fi

    if [ "$VM_IS_CTBOT" = 1 ]; then
      copy_files

      setup_ct_swarming_bot
    fi

    reboot

    if [[ $FAILED ]]; then
      echo
      echo "FAILURES: $FAILED"
      echo "Please manually fix these errors."
      echo
    fi
  fi
done

cat <<INP

Instances are ready to use.

Note:
If you created windows instances then please do the following:
* Log in and open the Windows update service, click on "Change settings" and select
  "Download updates but let me choose whether to install them".

INP
