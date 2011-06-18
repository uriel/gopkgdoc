#!/bin/sh
curl --data-urlencode payload@`dirname $0`/test.json http://localhost:8080/hook/github
