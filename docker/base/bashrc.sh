# shellcheck shell=bash
# Build environment for build-deps.sh. Loaded through BASH_ENV because the flags
# depend on `uname -m`.
#
# Forked from imgproxy-docker-base (MIT, Copyright (c) 2017 Sergey Alexandrovich);
# see LICENSE.imgproxy-docker-base. Changes: /opt/xtandard prefix, hardening flags.

case "$(uname -m)" in
  x86_64)
    export CFLAGS="-msse4"
    ;;

  aarch64)
    export CFLAGS="-march=armv8.2-a+fp16"
    ;;
esac

export XI_PREFIX=/opt/xtandard
export PATH="$XI_PREFIX/bin:/root/.cargo/bin:/root/.python/bin:$PATH"
export PKG_CONFIG_LIBDIR=$XI_PREFIX/lib/pkgconfig
export CMAKE_PREFIX_PATH=$XI_PREFIX
# Some older CMake projects declare cmake_minimum_required < 3.5, rejected by CMake 4.
export CMAKE_POLICY_VERSION_MINIMUM=3.5

# Ubuntu's GCC already enables -D_FORTIFY_SOURCE=3 when optimising; stack protector is
# spelled out so it survives projects that reset the default flags.
export CFLAGS="$CFLAGS -Os -fPIC -fstack-protector-strong -D_GLIBCXX_USE_CXX11_ABI=1 -fno-asynchronous-unwind-tables -ffunction-sections -fdata-sections"
export CXXFLAGS=$CFLAGS
export CPPFLAGS="${CPPFLAGS:-} -I$XI_PREFIX/include"
export LDFLAGS="${LDFLAGS:-} -L$XI_PREFIX/lib -Wl,--gc-sections -Wl,-z,relro,-z,now -Wl,-rpath,$XI_PREFIX/lib"
