#!/bin/bash

RootPath=$(cd $(dirname $0); pwd)


build_linux_x86_64() {
     make

}

# build arm64 with amd64 docker ubuntu:focal, apt-get install -y gcc-9-aarch64-linux-gnu gcc-aarch64-linux-gnu  g++-9-aarch64-linux-gnu g++-aarch64-linux-gnu 
# Support Ubuntu focal, not support CentOS7
build_linux_arm64_gcc9() {
    echo "build linux arm64 gcc9"
    get_rocksdb_compress_dep
    export PORTABLE=1
    export ARCH=arm64
 #   export CC=aarch64-linux-gnu-gcc        
    export EXTRA_CFLAGS="-Wno-error=deprecated-copy -fno-strict-aliasing -Wclass-memaccess -Wno-error=class-memaccess -Wpessimizing-move -Wno-error=pessimizing-move"
    export EXTRA_CXXFLAGS=$EXTRA_CFLAGS

    CGO_ENABLED=1 GOOS=linux GOARCH=arm64 make
  

}

# build arm64 with amd64 docker buntu:xenial , apt-get install -y gcc-4.9-aarch64-linux-gnu gcc-aarch64-linux-gnu  g++-4.9-aarch64-linux-gnu g++-aarch64-linux-gnu 
# support CentOS7 
#
build_linux_arm64_gcc4() {
    echo "build linux arm64 gcc4.9"
    get_rocksdb_compress_dep
    export PORTABLE=1
    export ARCH=arm64
 #   export CC=aarch64-linux-gnu-gcc        
    export EXTRA_CFLAGS=" -fno-strict-aliasing  "
    export EXTRA_CXXFLAGS=$EXTRA_CFLAGS

    CGO_ENABLED=1 GOOS=linux GOARCH=arm64 make
    

}

# wget compress dep 
get_rocksdb_compress_dep() {

    if [ ! -d "{RootPath}/vendor/dep" ]; then
        mkdir -p {RootPath}/vendor/dep
        cd {RootPath}/vendor/dep
        wget http://www.zlib.net/zlib-1.2.11.tar.gz
        wget https://astuteinternet.dl.sourceforge.net/project/bzip2/bzip2-1.0.6.tar.gz
        wget https://codeload.github.com/facebook/zstd/zip/v1.4.5
        wget https://codeload.github.com/lz4/lz4/tar.gz/v1.9.2

        tar zxf zlib-1.2.11.tar.gz
        tar zxf bzip2-1.0.6.tar.gz
        unzip v1.4.5
        tar zxf v1.9.2

        #rm -rf zlib-1.2.11.tar.gz bzip2-1.0.6.tar.gz v1.4.5 v1.9.2
        cd ${RootPath}
        
    fi
    cd ${RootPath}

}


CPUTYPE=${CPUTYPE} | tr 'A-Z' 'a-z'
echo ${CPUTYPE}
case ${CPUTYPE} in
    "arm64_gcc9")
        build_linux_arm64_gcc9
        ;;
    "arm64_gcc4")
        build_linux_arm64_gcc4
        ;;
    "arm64")
        build_linux_arm64_gcc4
        ;;
    *)
        build_linux_x86_64
        ;;
esac
