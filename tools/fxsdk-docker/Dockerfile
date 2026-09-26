# fxSDK + gint cross-toolchain for building fx-CG50 add-ins (.g3a) on this Windows box via
# Docker. Follows the documented GiteaPC route (Lephenixnoir): sh-elf-gcc (binutils+gcc for
# SuperH), fxsdk, OpenLibm, fxlibc, gint. The gcc build takes ~20-40 min; the image is reused.
#
#   docker build -t fxsdk:latest tools/fxsdk-docker
#   docker run --rm -v "/F/ru/myprojects/may/cg50/tools/keyprobe:/work" fxsdk:latest fxsdk build-cg
#
FROM debian:12

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        git ca-certificates curl build-essential cmake ninja-build pkg-config python3 python3-pil \
        libpng-dev libusb-1.0-0-dev libudev-dev libsdl2-dev \
        libgmp-dev libmpfr-dev libmpc-dev libisl-dev zlib1g-dev texinfo flex bison \
    && rm -rf /var/lib/apt/lists/*
# fxsdk's configure also wants udisks2 (fxlink mass-storage mode); separate layer keeps the
# big one above cached.
RUN apt-get update && apt-get install -y --no-install-recommends libudisks2-dev libglib2.0-dev libncurses-dev \
    && rm -rf /var/lib/apt/lists/*

# GiteaPC = the forge's package manager; everything installs under /root/.local
ENV PATH="/root/.local/bin:${PATH}" \
    PKG_CONFIG_PATH="/root/.local/lib/pkgconfig" \
    CMAKE_PREFIX_PATH="/root/.local"
RUN git clone --depth 1 https://gitea.planet-casio.com/Lephenixnoir/GiteaPC.git /opt/GiteaPC \
    && cd /opt/GiteaPC && make install

# toolchain + SDK + libraries (order matters; giteapc resolves deps but this is explicit)
RUN giteapc install -y Lephenixnoir/fxsdk Lephenixnoir/sh-elf-gcc
RUN giteapc install -y Lephenixnoir/OpenLibm Vhex-Kernel-Core/fxlibc Lephenixnoir/gint

WORKDIR /work
