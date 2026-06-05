# Cross-compiles the Go emulator core (emu_go/) into libcg50core.so for Android, one per
# ABI, dropping them into app/src/main/jniLibs/<abi>/ where Gradle packages them. The cgo
# C ABI lives in emu_go/android_bridge.go (build tag `android`, auto-set by GOOS=android).
# Run this whenever the Go core changes; then build the APK in Android Studio.
#
#   pwsh -File android/build_go_lib.ps1
#
# Requires: Go on PATH + the Android NDK (path below; edit if your NDK version differs).

$ErrorActionPreference = "Stop"

$ndkBin = "D:/files/Android_SDK/ndk/28.2.13676358/toolchains/llvm/prebuilt/windows-x86_64/bin"
$api    = 30                                   # must be <= app minSdk (30)
$emu    = Join-Path $PSScriptRoot "../emu_go"  # the Go package dir
$jni    = Join-Path $PSScriptRoot "app/src/main/jniLibs"

$targets = @(
    @{ abi = "arm64-v8a"; goarch = "arm64"; cc = "$ndkBin/aarch64-linux-android$api-clang.cmd" },
    @{ abi = "x86_64";    goarch = "amd64"; cc = "$ndkBin/x86_64-linux-android$api-clang.cmd" }
)

foreach ($t in $targets) {
    $cc = $t.cc
    if (-not (Test-Path $cc)) { throw "NDK clang not found: $cc" }
    $out = Join-Path $jni $t.abi
    New-Item -ItemType Directory -Force -Path $out | Out-Null

    $env:CGO_ENABLED = "1"
    $env:GOOS        = "android"
    $env:GOARCH      = $t.goarch
    $env:CC          = $cc

    Write-Host "Building libcg50core.so for $($t.abi) (GOARCH=$($t.goarch)) ..."
    & go build -C $emu -buildmode=c-shared -o (Join-Path $out "libcg50core.so") .
    if ($LASTEXITCODE -ne 0) { throw "go build failed for $($t.abi)" }
    # The c-shared header isn't needed (native-lib.cpp declares the prototypes); drop it.
    Remove-Item (Join-Path $out "libcg50core.h") -ErrorAction SilentlyContinue
    Write-Host "  -> $out/libcg50core.so"
}

Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH, Env:CC -ErrorAction SilentlyContinue
Write-Host "Done. Rebuild the app in Android Studio to repackage the .so files."
