Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=../../bin/app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=../../bin/app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=../../bin/app-amd64.exe -DARG_WAILS_ARM64_BINARY=../../bin/app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "Snet"
## !define INFO_COMPANYNAME    "My Company" # Default "SNET"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "Snet"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "1.2.6"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "© 2026, My Company"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
## !define WAILS_INSTALL_SCOPE     "user"             # Default "machine" - set to "user" for per-user install ($LOCALAPPDATA) without UAC prompt
####
## Include the wails tools
####
!include "wails_tools.nsh"
!include "LogicLib.nsh"
!include "StrFunc.nsh"
${Using:StrFunc} StrStr

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"
!include "nsDialogs.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_CONFIRM # Confirm the uninstall.
UninstPage custom un.DataPage un.DataPageLeave # Keep data by default.
!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

Var DeleteAllDataCheckbox
Var DeleteAllData

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-Setup-v${INFO_PRODUCTVERSION}.exe" # Name of the installer's file.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

# Uninstall data policy: Snet's device identity, networks and private key live
# in ProgramData. They must survive an uninstall so reinstalling does not turn
# this machine into a new device or lose access to its networks. Only an
# explicit checkbox may erase them.
Function un.DataPage
    !insertmacro MUI_HEADER_TEXT "卸载 Snet" "选择是否保留本地数据"
    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    ${NSD_CreateLabel} 0 0 100% 28u "默认保留设备 ID、网络配置和密钥。重新安装后，Snet 会继续使用同一台设备的身份并自动恢复网络。"
    Pop $0
    ${NSD_CreateCheckbox} 0 38u 100% 14u "同时删除所有本地数据（包括设备 ID，删除后无法恢复历史网络）"
    Pop $DeleteAllDataCheckbox
    ${NSD_SetState} $DeleteAllDataCheckbox ${BST_UNCHECKED}
    nsDialogs::Show
FunctionEnd

Function un.DataPageLeave
    ${NSD_GetState} $DeleteAllDataCheckbox $DeleteAllData
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    # Upgrade path: stop the GUI/server before extracting. A running service
    # keeps C:\Program Files\SNET\snetd.exe locked (the classic "Error opening
    # file for writing" during an over-install). Try the uninstaller first,
    # then force-stop any surviving process/service as a fallback.
    ${If} ${FileExists} "$INSTDIR\snetd.exe"
        nsExec::ExecToLog '"$INSTDIR\snetd.exe" uninstall'
        Pop $0
    ${EndIf}
    # Expected to fail on a first install (service absent). Use ExecToStack so
    # these benign 1060 errors do not pollute the installation log.
    nsExec::ExecToStack '"$SYSDIR\sc.exe" stop SNetDaemon'
    Pop $0
    Pop $1
    Sleep 1200
    nsExec::ExecToStack '"$SYSDIR\sc.exe" delete SNetDaemon'
    Pop $0
    Pop $1
    nsExec::ExecToStack '"$SYSDIR\sc.exe" query SNetDaemon'
    Pop $0
    Pop $1
    ${StrStr} $2 "$1" "DELETE_PENDING"
    ${If} $2 != ""
        MessageBox MB_ICONEXCLAMATION|MB_OKCANCEL "检测到旧服务正在后台清理，建议重启电脑后再安装。$\r$\n$\r$\n点击「确定」忽略并继续，点击「取消」退出安装。" /SD IDOK IDOK +2
        Abort
    ${EndIf}
    nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM snetd.exe /T'
    Pop $0
    Pop $1
    nsExec::ExecToStack '"$SYSDIR\taskkill.exe" /F /IM Snet.exe'
    Pop $0
    Pop $1
    Delete /REBOOTOK "$INSTDIR\SnetWindows.exe"
    Sleep 2000

    # Try to move a locked old daemon aside. If the rename succeeds, the file
    # handle is released and the new binary can be written. Mark the old file
    # for deletion after reboot as a second line of defence.
    ${If} ${FileExists} "$INSTDIR\snetd.exe"
        Delete /REBOOTOK "$INSTDIR\snetd.exe.old"
        ClearErrors
        Rename "$INSTDIR\snetd.exe" "$INSTDIR\snetd.exe.old"
        ${If} ${Errors}
            Delete /REBOOTOK "$INSTDIR\snetd.exe"
            MessageBox MB_ICONEXCLAMATION|MB_OK "旧版 snetd.exe 仍被占用。安装将继续，但请重启电脑后重新运行安装包以完成后台服务更新。" /SD IDOK
        ${Else}
            Delete /REBOOTOK "$INSTDIR\snetd.exe.old"
        ${EndIf}
    ${EndIf}

    !insertmacro wails.files

    ClearErrors
    File /nonfatal "/oname=snetd.exe" "..\..\..\bin\snetd.exe"
    IfErrors 0 +2
        MessageBox MB_ICONEXCLAMATION|MB_OK "新版 snetd.exe 未能写入。请重启电脑后重新运行安装包。" /SD IDOK
    File "/oname=snetctl.exe" "..\..\..\bin\snetctl.exe"
    File "/oname=wintun.dll" "..\..\..\bin\wintun.dll"

    nsExec::ExecToLog '"$INSTDIR\snetd.exe" install'
    Pop $0
    ${If} $0 != 0
        MessageBox MB_ICONEXCLAMATION|MB_OK "SNET 后台服务安装失败（代码 $0）。请以管理员身份重新运行安装包。" /SD IDOK
    ${EndIf}

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    # Remove legacy SnetWindows shortcuts/registration left by pre-1.1.8 builds.
    Delete "$SMPROGRAMS\SnetWindows.lnk"
    Delete "$DESKTOP\SnetWindows.lnk"
    DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\SNETSnetWindows"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\SNETSnetWindows"

    # Refresh the shell's icon cache so an upgraded install immediately shows
    # the current Snet icon instead of the previous Wails artwork.
    nsExec::ExecToStack '"$SYSDIR\ie4uinit.exe" -show'
    Pop $0
    Pop $1
    System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, i 0, i 0)'

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols
    
    !insertmacro wails.writeUninstaller

    IfSilent 0 +2
        Exec '"$WINDIR\explorer.exe" "$INSTDIR\${PRODUCT_EXECUTABLE}"'
SectionEnd

Section "uninstall" 
    !insertmacro wails.setShellContext

    # Remove the service first so snetd.exe is not locked while $INSTDIR is
    # deleted. The service keeps no data outside ProgramData.
    nsExec::ExecToStack '"$INSTDIR\snetd.exe" uninstall'
    Pop $0
    Pop $1

    # Never remove the device identity or network config unless the user
    # explicitly selected the destructive option above. Losing device.id is
    # what made a reinstall look like a brand-new device and lose history.
    ${If} $DeleteAllData == ${BST_CHECKED}
        ReadEnvStr $9 "PROGRAMDATA"
        ${If} $9 == ""
            StrCpy $9 "C:\ProgramData"
        ${EndIf}
        RMDir /r "$9\SNET"
        RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath
    ${EndIf}

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    Delete "$SMPROGRAMS\SnetWindows.lnk"
    Delete "$DESKTOP\SnetWindows.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
