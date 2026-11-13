"""Package the CI build without publishing a release."""
import hashlib
import os
from pathlib import Path
import zipfile

if os.environ["INSTALLER_TARGET"] != "linux-amd64" or os.environ["INSTALLER_BINARY"] != "kora":
    raise SystemExit("Only Linux x86-64 releases are supported")
binary = Path("dist") / "kora"
archive = binary.parent / f"kora_installer-{os.environ['INSTALLER_TARGET']}.zip"
with zipfile.ZipFile(archive, "x", compression=zipfile.ZIP_DEFLATED) as output:
    output.write(binary, binary.name)
    output.write("README.md")
    output.write("template.example.json")
    output.write("templates/base.json")
    output.write("assets/txadmin-recipe.yaml")
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
with (binary.parent / "SHA256SUMS").open("x", encoding="utf-8") as output:
    output.write(f"{digest}  {archive.name}\n")

# Stable raw asset names consumed by install.sh, in addition to downloadable ZIPs.
import shutil
asset_name = "kora-linux-amd64"
asset = binary.parent / asset_name
shutil.copyfile(binary, asset)
asset.chmod(0o755)
asset.with_name(asset.name + ".sha256").write_text(hashlib.sha256(asset.read_bytes()).hexdigest() + "  " + asset.name + "\n", encoding="utf-8")
