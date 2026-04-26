export ANDROID_SDK_ROOT=/home/nalek0/Android/Sdk
export ANDROID_NDK_HOME=$ANDROID_SDK_ROOT/ndk/27.0.12077973
export PATH=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin:$PATH
export DEBUG_PREFIX_FLAGS="-fdebug-prefix-map=$ANDROID_SDK_ROOT=/android-sdk -fdebug-prefix-map=$ANDROID_NDK_HOME=/android-ndk"
export CGO_CFLAGS="$DEBUG_PREFIX_FLAGS ${CGO_CFLAGS:-}"
export CGO_LDFLAGS="$DEBUG_PREFIX_FLAGS ${CGO_LDFLAGS:-}"
export CC=aarch64-linux-android21-clang
export CXX=aarch64-linux-android21-clang++
export CGO_ENABLED="1"
export GOOS="android"
export GOARCH="arm64"

echo "[+] Building .so library"
/usr/local/go/bin/go build -tags linux -ldflags="-buildid=" -v -trimpath -buildvcs=false -o libbackend.so -buildmode c-shared ./kotlin_exports/

echo "[+] Move .so library"
mkdir -p ../kmp_module/backend/src/main/cpp/include/arm64-v8a/
mkdir -p ../kmp_module/backend/src/main/jniLibs/arm64-v8a
cp libbackend.so ../kmp_module/backend/src/main/jniLibs/arm64-v8a/libbackend.so
cp libbackend.h ../kmp_module/backend/src/main/cpp/include/arm64-v8a/libbackend.h
