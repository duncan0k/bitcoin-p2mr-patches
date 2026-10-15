import io
import stat
import unittest
import zipfile

import package


class PackageTests(unittest.TestCase):
    def test_macos_zip_modes(self):
        programs = {"Ark0Witness", "bitcoind"}
        files = [(name, name.encode("ascii")) for name in (
            "Ark0Witness", "bitcoind", "README.txt", "COPYING.txt",
            "LICENSE-mldsa-native.txt", "LICENSE-Ark0Witness.txt")]
        out = io.BytesIO()
        top = f"ark0-witness-{package.VERSION}-macos/"
        package.pack_zip(out, top, files, programs, "macos")
        with zipfile.ZipFile(out) as archive:
            self.assertEqual(archive.namelist(), [top + name for name, _ in files])
            for entry, (name, data) in zip(archive.infolist(), files):
                self.assertEqual(entry.create_system, 3)
                self.assertEqual(entry.external_attr >> 16,
                                 stat.S_IFREG | (0o755 if name in programs else 0o644))
                self.assertEqual(archive.read(entry), data)

    def test_windows_zip_bytes_unchanged(self):
        files = [("Ark0Witness.exe", b"witness\x00"), ("bitcoind.exe", b"node\xff"),
                 ("README.txt", b"readme\r\n"), ("COPYING.txt", b"copying\r\n"),
                 ("LICENSE-mldsa-native.txt", b"mldsa\r\n"),
                 ("LICENSE-Ark0Witness.txt", b"license\r\n")]
        # Preserve the original Windows writer as the byte-for-byte reference.
        original = io.BytesIO()
        with zipfile.ZipFile(original, "w") as archive:
            for name, data in files:
                entry = zipfile.ZipInfo(package.TOP + name, package.STAMP)
                entry.compress_type = zipfile.ZIP_DEFLATED
                entry.create_system = 0
                entry.external_attr = 0
                archive.writestr(entry, data, compresslevel=9)
        out = io.BytesIO()
        package.pack_zip(out, package.TOP, files,
                         {"Ark0Witness.exe", "bitcoind.exe"}, "windows")
        self.assertEqual(out.getvalue(), original.getvalue())


if __name__ == "__main__":
    unittest.main()
