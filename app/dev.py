"""Run CG50 Studio in development mode (live reload of frontend/) or build it.

    python app/dev.py               wails dev   (window + http://localhost:34115 in a browser)
    python app/dev.py dev --hidden  the same without showing the window (testing through the
                                    browser page without taking the user's focus)
    python app/dev.py dev --min     the window starts minimized
    python app/dev.py build         wails build (app/build/bin/)
    python app/dev.py run --hidden  run the built app (also --min)

Runs the wails CLI from app/ (it has no -C flag), found on PATH or in $(go env GOPATH)/bin.
"""
import os
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


def wails():
    w = shutil.which("wails")
    if w:
        return w
    gopath = subprocess.run(["go", "env", "GOPATH"], capture_output=True, text=True).stdout.strip()
    exe = os.path.join(gopath, "bin", "wails.exe" if os.name == "nt" else "wails")
    if os.path.exists(exe):
        return exe
    sys.exit("wails not found: go install github.com/wailsapp/wails/v2/cmd/wails@latest")


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "dev"
    rest = sys.argv[2:]
    appargs = []
    if "--hidden" in rest:
        rest.remove("--hidden")
        appargs.append("-hidden")
    if "--min" in rest:
        rest.remove("--min")
        appargs.append("-minimized")
    if cmd == "run":
        exe = os.path.join(HERE, "build", "bin", "cg50studio.exe" if os.name == "nt" else "cg50studio")
        if not os.path.exists(exe):
            sys.exit("not built yet: python app/dev.py build")
        sys.exit(subprocess.run([exe] + appargs + rest).returncode)
    args = [wails(), cmd] + rest
    if cmd == "dev" and appargs:
        args += ["-appargs", " ".join(appargs)]
    if cmd == "build":
        args += ["-clean"] if "-clean" not in args else []
    sys.exit(subprocess.run(args, cwd=HERE).returncode)


if __name__ == "__main__":
    main()
