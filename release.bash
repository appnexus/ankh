#!/bin/bash

set -e

if [ -z "$VERSION" ] ; then
	echo "Must provide VERSION"
	exit 1
fi

make clean || exit 1

release_dir=release
mkdir -p $release_dir

function release() {
	export GOOS=$1
	export GOARCH=$2

	echo "Building for GOOS=${GOOS} and GOARCH=${GOARCH}"

	targz=ankh-${GOOS}-${GOARCH}.tar.gz
	rm -f ${release_dir}/ankh ${release_dir}/${targz}
	env GOOS=${GOOS} GOARCH=${GOARCH} make
	file ankh/ankh && mv -f ankh/ankh ${release_dir}/ankh
	(cd $release_dir && tar cvfz ${targz} ankh && ls -lrt && rm -f ankh) || exit 1
}

release linux amd64

release darwin amd64
