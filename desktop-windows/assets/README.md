# Windows assets

`wintun.dll` is **not committed** to this repository.

Download the official 64-bit Wintun DLL (0.14.1) and either:

- place it here manually as `desktop-windows/assets/wintun.dll`, or
- run the verified downloader:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\desktop-windows\scripts\fetch-wintun.ps1
```

The build copies the DLL next to `Snet.exe` and `snetd.exe`. Wintun-go
loads it from the executable's application directory.

SHA256:

```text
e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce  wintun.dll
```
