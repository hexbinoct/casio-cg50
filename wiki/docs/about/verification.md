# How facts are verified

Facts on this site come from three places. Each page marks which one backs each claim.

## Reading the OS

The OS 3.60 image (dumped from a real calculator) is loaded in Ghidra at `0x80000000`, its
address when running. Addresses on this site are those runtime addresses.

Reading code tells you what the OS *does* with the hardware, not how the hardware
*responds*. Facts that only come from here are marked **Unconfirmed**.

## The emulator

[casio-cg50](https://github.com/hexbinoct/casio-cg50) boots the real OS from reset and runs
its apps and add-ins. If the OS or an add-in only works when the emulator behaves a certain
way, that is good evidence for the behaviour, and a test in the emulator's suite keeps it.

The emulator has two implementations, held to each other:

- a **Python** emulator, the reference, slow and simple;
- a **Go** emulator, the fast one, which must match the Python one instruction for
  instruction on a frozen boot trace and a suite of CPU test cases.

Both can be wrong the same way, which is why hardware measurements come first.

## The real calculator

Small add-ins, built with the fxSDK and gint, run on a real fx-CG50 and write their
measurements to a file or send them over USB. These are the strongest evidence on the
site. The results are kept in the repository under `os/devic_probes/`.

!!! danger "Probes can reset your calculator"
    A probe that put the CPU to sleep after touching the clock generator reset a calculator
    to its first-boot setup. Files in storage memory survived; everything in RAM did not.
    Read-only register sampling has been safe.
