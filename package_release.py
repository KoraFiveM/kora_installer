"""Package the CI build without publishing a release."""
import hashlib
import os
from pathlib import Path
import zipfile

target = os.environ["INSTALLER_TARGET"]
expected = {"linux-amd64": "kora", "windows-amd64": "kora_installer.exe"}
if target not in expected or os.environ["INSTALLER_BINARY"] != expected[target]:
    raise SystemExit("Unsupported release target/binary")
binary = Path("dist") / expected[target]
archive = binary.parent / f"kora_installer-{os.environ['INSTALLER_TARGET']}.zip"
with zipfile.ZipFile(archive, "x", compression=zipfile.ZIP_DEFLATED) as output:
    output.write(binary, binary.name)
    if target == "windows-amd64":
        output.write(binary.parent / "kora-control.exe", "kora-control.exe")
    output.write("README.md")
    output.write("template.example.json")
    output.write("templates/base.json")
    output.write("assets/txadmin-recipe.yaml")
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
with (binary.parent / "SHA256SUMS").open("x", encoding="utf-8") as output:
    output.write(f"{digest}  {archive.name}\n")

# Stable raw asset names consumed by install.sh, in addition to downloadable ZIPs.
import shutil
asset_name = "kora-linux-amd64" if target == "linux-amd64" else "kora_installer.exe"
asset = binary.parent / asset_name
if binary != asset:
    shutil.copyfile(binary, asset)
asset.chmod(0o755)
asset.with_name(asset.name + ".sha256").write_text(hashlib.sha256(asset.read_bytes()).hexdigest() + "  " + asset.name + "\n", encoding="utf-8")

if target == "windows-amd64":
    gui = binary.parent / "kora-control.exe"
    gui.with_name(gui.name + ".sha256").write_text(hashlib.sha256(gui.read_bytes()).hexdigest() + "  " + gui.name + "\n", encoding="utf-8")
