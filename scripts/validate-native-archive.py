"""Validate native-game release input before extracting it in the signing job."""
from pathlib import PurePosixPath
from stat import S_IFDIR, S_IFREG, S_IFMT
import sys
from zipfile import ZipFile


def validate(archive, name, pck):
    root = f"{name}.app/Contents"
    executable = f"{root}/MacOS/{name}"
    pack = f"{root}/Resources/{pck}"
    # A pure Godot export contains one executable, its pack, and bundle metadata.
    allowed = {executable, pack, f"{root}/Info.plist", f"{root}/PkgInfo",
               f"{root}/Resources/icon.icns", f"{root}/Resources/PrivacyInfo.xcprivacy",
               f"{root}/_CodeSignature/CodeResources"}
    with ZipFile(archive) as z:
        seen = set()
        files = set()
        total = 0
        for entry in z.infolist():
            path = PurePosixPath(entry.filename)
            if (not path.parts or path.parts[0] != name + ".app" or path.is_absolute() or ".." in path.parts
                    or "\\" in entry.filename or S_IFMT(entry.external_attr >> 16) not in (0, S_IFDIR, S_IFREG)
                    or path.as_posix().casefold() in seen):
                raise ValueError("Unexpected or unsafe native game ZIP entry")
            seen.add(path.as_posix().casefold())
            total += entry.file_size
            if total > 384 * 1024 * 1024:
                raise ValueError("Native game ZIP exceeds the expanded size limit")
            if not entry.is_dir():
                files.add(entry.filename)
        if executable not in files:
            raise ValueError("The ZIP is missing its executable")
        if pck != "-":
            if pack not in files:
                raise ValueError("Missing Godot pack")
            if files - allowed:
                raise ValueError("Unexpected payload in pure Godot app")
            with z.open(pack) as data:
                if data.read(4) != b"GDPC":
                    raise ValueError("Invalid Godot pack header")


if __name__ == "__main__":
    validate(*sys.argv[1:])
