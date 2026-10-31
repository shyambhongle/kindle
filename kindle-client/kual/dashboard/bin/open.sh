#!/bin/sh
# Opens the Kindle browser at the dashboard URL.
# Edit URL below. Use your backend address, or file:///mnt/us/dashboard/index.html for a local copy.
URL="http://192.168.1.20:8080/"

lipc-set-prop com.lab126.appmgrd start "app://com.lab126.browser?url=${URL}"
