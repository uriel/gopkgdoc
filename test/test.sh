#!/bin/sh
userRepo=${1:-garyburd/go-mongo}
payload="{ \"ref\": \"refs/heads/master\", \"repository\": { \"url\": \"https://github.com/${userRepo}\" } }"
server=${2:-localhost:8080}
set -x
curl --data-urlencode payload="${payload}" http://${server}/hook/github
