# SPDX-License-Identifier: Apache-2.0
#
# Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.

BUILDDIR ?= $(CURDIR)/build
DESTDIR ?= $(CURDIR)/out

NDK_GO_ARCH_MAP_x86 := 386
NDK_GO_ARCH_MAP_x86_64 := amd64
NDK_GO_ARCH_MAP_arm := arm
NDK_GO_ARCH_MAP_arm64 := arm64
NDK_GO_ARCH_MAP_mips := mipsx
NDK_GO_ARCH_MAP_mips64 := mips64x

comma := ,
CLANG_FLAGS := --target=$(TARGET) --sysroot=$(SYSROOT)
export CGO_CFLAGS := $(CLANG_FLAGS) $(subst -mthumb,-marm,$(CFLAGS))

# Android 15+ may run on 16KB-page kernels. On those devices, 64-bit ELF LOAD
# segments must be linked with 16KB-compatible page alignment or the loader can
# crash inside the library at/near load time.
#
# NDK r28+ enables this by default for many toolchains, but since we drive the
# linker via cgo we also explicitly set it here for 64-bit Android ABIs.
ifeq ($(ANDROID_ARCH_NAME),arm64)
  PAGE_SIZE_LDFLAGS := -Wl,-z,common-page-size=16384 -Wl,-z,max-page-size=16384
else ifeq ($(ANDROID_ARCH_NAME),x86_64)
  PAGE_SIZE_LDFLAGS := -Wl,-z,common-page-size=16384 -Wl,-z,max-page-size=16384
else
  PAGE_SIZE_LDFLAGS :=
endif

export CGO_LDFLAGS := $(CLANG_FLAGS) $(PAGE_SIZE_LDFLAGS) $(patsubst -Wl$(comma)--build-id=%,-Wl$(comma)--build-id=none,$(LDFLAGS)) -Wl,-soname=libwg-go.so
export GOARCH := $(NDK_GO_ARCH_MAP_$(ANDROID_ARCH_NAME))
export GOOS := android
export CGO_ENABLED := 1

# Use GO passed from the caller, fall back to system go
GO ?= go

default: $(DESTDIR)/libwg-go.so

$(DESTDIR)/libwg-go.so:
	$(GO) build -tags linux -ldflags="-X github.com/amnezia-vpn/amneziawg-go/ipc.socketDirectory=/data/data/$(ANDROID_PACKAGE_NAME)/cache/amneziawg -buildid=" -v -trimpath -buildvcs=false -o "$@" -buildmode c-shared

.DELETE_ON_ERROR:
