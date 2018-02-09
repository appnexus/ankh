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

	bin_src_path=bin/ankh
	if [ ! -z "$3" ] ; then
		bin_src_path=bin/${3}/ankh
	fi

	echo "Building for GOOS=${GOOS} and GOARCH=${GOARCH} using binary from ${bin_src_path}"

	bin=ankh-${GOOS}-${GOARCH}
	targz=${bin}.tar.gz
	rm -f ${release_dir}/${bin} ${release_dir}/${targz}
	env GOOS=${GOOS} GOARCH=${GOARCH} make && cp ${bin_src_path} ${release_dir}/${bin} && (cd $release_dir && tar cvfz ${targz} ${bin}) || exit 1
}

release linux amd64 $(uname -a | grep -q Darwin 2>/dev/null && echo "linux_amd64" || echo "")

release darwin amd64 $(uname -a | grep -v -q Darwin 2>/dev/null && echo "darwin_amd64" || echo "")
