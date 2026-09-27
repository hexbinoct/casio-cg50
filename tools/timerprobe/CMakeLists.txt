# fxSDK add-in project (fx-CG50 only). Build inside the fxsdk:latest container:
#   docker run --rm -v "/F/ru/myprojects/may/cg50/tools/timerprobe:/work" fxsdk:latest fxsdk build-cg
cmake_minimum_required(VERSION 3.15)
project(timerprobe)

include(GenerateG3A)
include(Fxconv)
find_package(Gint 2.1 REQUIRED)

set(SOURCES src/main.c)

add_executable(timerprobe ${SOURCES})
target_compile_options(timerprobe PRIVATE -Wall -Wextra -Os)
target_link_libraries(timerprobe Gint::Gint)

generate_g3a(TARGET timerprobe OUTPUT "TIMERPRB.g3a"
  NAME "TimerProbe" ICONS assets-cg/icon-uns.png assets-cg/icon-sel.png)
