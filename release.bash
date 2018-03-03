#!/bin/bash

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

	bin_src_path=ankh/ankh
	echo "Building for GOOS=${GOOS} and GOARCH=${GOARCH} using binary from ${bin_src_path}"

	bin=ankh-${GOOS}-${GOARCH}
	targz=${bin}.tar.gz
	rm -f ${release_dir}/${bin} ${release_dir}/${targz}
	env GOOS=${GOOS} GOARCH=${GOARCH} make && file ${bin_src_path} && cp ${bin_src_path} ${release_dir}/${bin} && (cd $release_dir && rm -f ankh && cp ${bin} ankh && tar cvfz ${targz} ankh && rm -f ankh) || exit 1
}

release linux amd64

release darwin amd64

make clean
