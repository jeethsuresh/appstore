#!/usr/bin/env python3
"""Enable Android's software navigation on the disposable CI touch device."""
import os
from pathlib import Path
import re

config = Path(os.environ["ANDROID_AVD_HOME"]) / "test.avd/config.ini"
lines = [line for line in config.read_text().splitlines() if not re.match(r"\s*hw\.mainKeys\s*=", line)]
config.write_text("\n".join(lines) + "\nhw.mainKeys=no\n")
print("Configured touch device: hw.mainKeys=no (software Back and edge gestures)")
