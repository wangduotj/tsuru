#!/bin/sh -e

go test ./...
go build -o websrv ./api/webserver
./websrv -dry=true -config=${PWD}/etc/tsuru.conf
go build -o collect ./collector/
./collect -dry=true
rm -f collect websrv
