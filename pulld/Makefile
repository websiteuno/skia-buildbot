# The list of files we want to go into core.js, which is concat'd and
# minified. These files should be either present in the project, brought
# into third_party/bower_compoents via bower, or in node_modules.
CORE_SOURCE_FILES = node_modules/native-promise-only/npo.js \
	third_party/bower_components/webcomponentsjs/webcomponents.min.js \
	../res/js/common.js

BOWER_DIR=third_party/bower_components

VULCANIZE1=true

default: core_js elements_html
	go install -v ./go/pulld

release: default
	./build_release "$(MESSAGE)"

release_not_gce: default
	go install -v ../get_service_account/go/get_service_account
	get_service_account
	./build_release_not_gce "$(MESSAGE)"

# You should run make release first
push_everywhere:
	pushcli --alsologtostderr --config_filename=../push/skiapush.conf pulld "*"


.PHONY: debug
debug: clean_webtools debug_core_js debug_elements_html

include ../webtools/webtools.mk
