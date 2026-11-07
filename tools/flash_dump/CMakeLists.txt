# The flash dumper add-in (gint). Build it in the fxSDK Docker image (tools/fxsdk-docker):
#   docker run --rm -v "F:\ru\myprojects\may\cg50\tools\flash_dump:/work" fxsdk:latest fxsdk build-cg
# -> FLASHDMP.g3a
cmake_minimum_required(VERSION 3.15)
project(flashdump)

include(GenerateG3A)
include(Fxconv)
find_package(Gint 2.1 REQUIRED)

set(SOURCES src/main.c)

add_executable(flashdump ${SOURCES})
target_compile_options(flashdump PRIVATE -Wall -Wextra -Os)
target_link_libraries(flashdump Gint::Gint)

generate_g3a(TARGET flashdump OUTPUT "FLASHDMP.g3a"
  NAME "FlashDump" ICONS assets-cg/icon-uns.png assets-cg/icon-sel.png)
