#!/usr/bin/env python
# Copyright (c) 2013 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.

"""Module that polls the skia-telemetry AppEngine WebApp.

Admin and Lua tasks are polled by this module. All new tasks are then triggered.
This module also periodically updates the Telemetry Information after
UPDATE_INFO_AFTER_SECS have elapsed.
"""


import json
import os
import subprocess
import sys
import tempfile
import time
import traceback
import urllib

import appengine_constants


SLEEP_BETWEEN_POLLS_SECS = 30

UPDATE_INFO_AFTER_SECS = 7200

# The following dictionaries ensure that tasks which are being currently
# processed are not triggered again.
ADMIN_ENCOUNTERED_KEYS = {}
CHROMIUM_BUILD_ENCOUNTERED_KEYS = {}
CHROMIUM_TRY_ENCOUNTERED_KEYS = {}
LUA_ENCOUNTERED_KEYS = {}
TELEMETRY_ENCOUNTERED_KEYS = {}
SKIA_TRY_ENCOUNTERED_KEYS = {}


def update_local_checkout():
  makefile_path = os.path.abspath(
      os.path.join(os.path.dirname(os.path.realpath(__file__)), os.pardir))
  old_cwd = os.getcwd()
  os.chdir(makefile_path)
  os.system("git pull; make all;")
  os.chdir(old_cwd)


def process_admin_task(task):
  # Extract required parameters.
  task_key = task['key']
  if task_key in ADMIN_ENCOUNTERED_KEYS:
    print '%s is already being processed' % task_key
    return
  ADMIN_ENCOUNTERED_KEYS[task_key] = 1

  task_name = task['task_name']
  username = task['username']
  pagesets_type = task['pagesets_type']

  cmd = []
  if task_name == appengine_constants.PAGESETS_ADMIN_TASK_NAME:
    cmd = [
        'create_pagesets_on_slaves',
        '--emails=' + username,
        '--gae_task_id=' + task_key,
        '--pageset_type=' + pagesets_type,
    ]
  elif task_name == appengine_constants.WEBPAGE_ARCHIVES_ADMIN_TASK_NAME:
    chromium_build_dir = get_chromium_build_dir(task['chromium_rev'],
                                                task['skia_rev'])
    cmd = [
        'capture_archives_on_slaves',
        '--emails=' + username,
        '--gae_task_id=' + task_key,
        '--pageset_type=' + pagesets_type,
        '--chromium_build=' + chromium_build_dir,
    ]
  print 'Updating local checkout'
  update_local_checkout()
  print 'Running admin cmd: ' + ' '.join(cmd)
  subprocess.Popen(cmd)


def process_chromium_build_task(task):
  # Extract required parameters.
  task_key = task['key']
  if task_key in CHROMIUM_BUILD_ENCOUNTERED_KEYS:
    print '%s is already being processed' % task_key
    return
  CHROMIUM_BUILD_ENCOUNTERED_KEYS[task_key] = 1

  chromium_rev = task['chromium_rev']
  skia_rev = task['skia_rev']
  username = task['username']
  run_id = '%s-%s' % (username.split('@')[0], time.time())

  print 'Updating local checkout'
  update_local_checkout()
  cmd = [
      'build_chromium',
      '--emails=' + username,
      '--gae_task_id=' + task_key,
      '--run_id=' + run_id,
      '--target_platform=Linux',
      '--apply_patches=false',
      '--chromium_hash=' + chromium_rev,
      '--skia_hash=' + skia_rev,
  ]
  print 'Running chromium build cmd: ' + ' '.join(cmd)
  subprocess.Popen(cmd)


def fix_and_write_patch(patch, run_id):
  """Modifies the patch for consumption by slaves and writes to local file."""
  # Remove all carriage returns, appengine adds them to blobs.
  patch_txt = patch.replace('\r\n', '\n')
  # Add an extra newline at the end because git sometimes rejects patches due to
  # missing newlines.
  patch_txt += '\n'
  patch_file = os.path.join(tempfile.gettempdir(),
                            '%s.patch' % run_id)
  f = open(patch_file, 'w')
  f.write(patch_txt)
  f.close()
  return patch_file


def process_skia_try_task(task):
  # Extract required parameters.
  task_key = task['key']
  if task_key in SKIA_TRY_ENCOUNTERED_KEYS:
    print '%s is already being processed' % task_key
    return
  SKIA_TRY_ENCOUNTERED_KEYS[task_key] = 1

  username = task['username']
  run_id = '%s-%s' % (username.split('@')[0], time.time())

  fix_and_write_patch(task['patch'], run_id)
  pagesets_type = task['pagesets_type']
  chromium_build_dir = get_chromium_build_dir(task['chromium_rev'],
                                              task['skia_rev'])
  render_pictures_args = task['render_pictures_args'].replace('"', r'\"')
  gpu_nopatch_run = task['gpu_nopatch_run']
  gpu_withpatch_run = task['gpu_withpatch_run']

  print 'Updating local checkout'
  update_local_checkout()
  skia_try_cmd = [
      'run_skia_correctness_on_workers',
      '--emails=' + username,
      '--gae_task_id=' + task_key,
      '--pageset_type=' + pagesets_type,
      '--chromium_build=' + chromium_build_dir,
      '--render_pictures_args=' + render_pictures_args,
      '--gpu_nopatch_run=' + gpu_nopatch_run,
      '--gpu_withpatch_run=' + gpu_withpatch_run,
      '--run_id=' + run_id,
  ]
  print 'Running skia try cmd: ' + ' '.join(skia_try_cmd)
  subprocess.Popen(skia_try_cmd)


def process_chromium_try_task(task):
  # Extract required parameters.
  task_key = task['key']
  if task_key in CHROMIUM_TRY_ENCOUNTERED_KEYS:
    print '%s is already being processed' % task_key
    return
  CHROMIUM_TRY_ENCOUNTERED_KEYS[task_key] = 1

  username = task['username']
  benchmark_name = task['benchmark_name']
  benchmark_arguments = task['benchmark_arguments']
  target_platform = task['target_platform']
  # Escape any quotes in benchmark arguments.
  benchmark_arguments = benchmark_arguments.replace('"', r'\"')
  num_repeated_runs = task['num_repeated_runs']
  variance_threshold = task['variance_threshold']
  discard_outliers = task['discard_outliers']
  pageset_type = task['pageset_type']
  browser_args_1 = task['browser_args_1']
  browser_args_2 = task['browser_args_2']
  # Copy the patches to a local file.
  run_id = '%s-%s' % (username.split('@')[0], time.time())
  fix_and_write_patch(task['chromium_patch'], run_id + '.chromium')
  fix_and_write_patch(task['blink_patch'], run_id + '.blink')
  fix_and_write_patch(task['skia_patch'], run_id + '.skia')

  print 'Updating local checkout'
  update_local_checkout()
  cmd = [
      'run_chromium_perf_on_workers',
      '--emails=' + username,
      '--gae_task_id=' + task_key,
      '--pageset_type=' + pageset_type,
      '--benchmark_name=' + benchmark_name,
      '--benchmark_extra_args=' + benchmark_arguments,
      '--browser_extra_args_nopatch=' + browser_args_1,
      '--browser_extra_args_withpatch=' + browser_args_2,
      '--repeat_benchmark=' + num_repeated_runs,
      '--target_platform=' + target_platform,
      '--run_id' + run_id,
      '--variance_threshold=' + variance_threshold,
      '--discard_outliers=' + discard_outliers,
  ]
  print 'Running chromium try cmd: ' + ' '.join(cmd)
  subprocess.Popen(cmd)


def process_lua_task(task):
  task_key = task['key']
  pagesets_type = task['pagesets_type']
  if task_key in LUA_ENCOUNTERED_KEYS:
    print '%s is already being processed' % task_key
    return
  LUA_ENCOUNTERED_KEYS[task_key] = 1
  chromium_build_dir = get_chromium_build_dir(task['chromium_rev'],
                                              task['skia_rev'])
  # Create a run id.
  run_id = '%s-%s' % (task['username'].split('@')[0], time.time())
  lua_file = os.path.join(tempfile.gettempdir(), '%s.lua' % run_id)
  f = open(lua_file, 'w')
  f.write(task['lua_script'])
  f.close()

  if task.get('lua_aggregator'):
    aggregator_file = os.path.join(tempfile.gettempdir(),
                                   '%s.aggregator' % run_id)
    f = open(aggregator_file, 'w')
    f.write(task['lua_aggregator'])
    f.close()

  print 'Updating local checkout'
  update_local_checkout()
  cmd = [
      'run_lua_on_workers',
      '--emails=' + task['username'],
      '--gae_task_id=' + task_key,
      '--pageset_type=' + pagesets_type,
      '--chromium_build=' + chromium_build_dir,
      '--run_id=' + run_id,
  ]
  print 'Running lua script cmd: ' + ' '.join(cmd)
  subprocess.Popen(cmd)


def process_telemetry_task(task):
  task_key = task['key']
  if task_key in TELEMETRY_ENCOUNTERED_KEYS:
    print '%s is already being processed' % task_key
    return
  TELEMETRY_ENCOUNTERED_KEYS[task_key] = 1
  benchmark_name = task['benchmark_name']
  benchmark_arguments = task['benchmark_arguments']
  # Escape any quotes in benchmark arguments.
  benchmark_arguments = benchmark_arguments.replace('"', r'\"')
  pagesets_type = task['pagesets_type']
  chromium_build_dir = get_chromium_build_dir(task['chromium_rev'],
                                              task['skia_rev'])
  username = task['username']
  # Create a run id.
  run_id = '%s-%s' % (username.split('@')[0], time.time())

  print 'Updating local checkout'
  update_local_checkout()
  cmd = [
      'run_benchmark_on_workers',
      '--emails=' + username,
      '--gae_task_id=' + task_key,
      '--pageset_type=' + pagesets_type,
      '--chromium_build=' + chromium_build_dir,
      '--benchmark_name=' + benchmark_name,
      '--benchmark_extra_args=' + benchmark_arguments,
      '--browser_extra_args=--disable-setuid-sandbox ' +
      '--enable-threaded-compositing --enable-impl-side-painting',
      '--repeat_benchmark=1',
      '--target_platform=Linux',
      '--run_id=' + run_id,
      '--tryserver_run=false',
  ]
  print 'Running telemetry cmd: ' + ' '.join(cmd)
  subprocess.Popen(cmd)


def get_chromium_build_dir(chromium_rev, skia_rev):
  """Construct the chromium build dir from chromium and skia revs."""
  return '%s-%s' % (chromium_rev[0:7], skia_rev[0:7])

TASK_TYPE_TO_PROCESSING_METHOD = {
    appengine_constants.ADMIN_TASK_NAME: process_admin_task,
    appengine_constants.CHROMIUM_BUILD_TASK_NAME: process_chromium_build_task,
    appengine_constants.CHROMIUM_TRY_TASK_NAME: process_chromium_try_task,
    appengine_constants.LUA_TASK_NAME: process_lua_task,
    appengine_constants.TELEMETRY_TASK_NAME: process_telemetry_task,
    appengine_constants.SKIA_TRY_TASK_NAME: process_skia_try_task,
}


class Poller(object):

  def Poll(self):
    info_updated_on = 0
    while True:
      try:
        if (time.time() - info_updated_on) >= UPDATE_INFO_AFTER_SECS:
          log_file = os.path.join(tempfile.gettempdir(), 'update-info.output')
          for cmd in ('bash vm_recover_slaves_from_crashes.sh',):
            script_name = cmd.split()[1]
            log_file = os.path.join(tempfile.gettempdir(), script_name)
            print '%s output will be available in %s' % (script_name, log_file)
            subprocess.Popen(cmd.split(), stdout=open(log_file, 'w'),
                             stderr=open(log_file, 'w'))
          info_updated_on = time.time()

        # pylint: disable=C0301
        oldest_pending_task_page = urllib.urlopen(
            appengine_constants.SKIA_TELEMETRY_WEBAPP +
            appengine_constants.GET_OLDEST_PENDING_TASK_SUBPATH)
        oldest_pending_task = json.loads(
            oldest_pending_task_page.read().replace('\r\n', '\\r\\n'))
        if oldest_pending_task:
          task_type = oldest_pending_task.keys()[0]
          processing_method = TASK_TYPE_TO_PROCESSING_METHOD[task_type]
          processing_method(oldest_pending_task[task_type])

        print 'Sleeping %s secs' % SLEEP_BETWEEN_POLLS_SECS
        time.sleep(SLEEP_BETWEEN_POLLS_SECS)
      except Exception:
        # The poller should never crash, output the exception and move on.
        print traceback.format_exc()
        continue


if '__main__' == __name__:
  sys.exit(Poller().Poll())
