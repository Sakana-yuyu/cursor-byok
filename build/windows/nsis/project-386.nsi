Unicode true

# 386 (x86) NSIS installer - does not depend on wails_tools.nsh
# wails_tools.nsh only supports AMD64/ARM64; this script handles 32-bit directly.
#
# Usage:
#   makensis -DARG_WAILS_386_BINARY=..\..\bin\cursor-byok-windows-386.exe project-386.nsi

!define INFO_PROJECTNAME "Cursor助手"
!define INFO_COMPANYNAME "Sakana"
!define INFO_PRODUCTNAME "Cursor助手"
!define INFO_COPYRIGHT "© 2026, Sakana"
!define PRODUCT_EXECUTABLE "${INFO_PROJECTNAME}.exe"
!define UNINST_KEY_NAME "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${UNINST_KEY_NAME}"
!define ARCH "386"
!ifndef INFO_PRODUCTVERSION
!define INFO_PRODUCTVERSION "0.0.0"
!endif
!define REQUEST_EXECUTION_LEVEL "admin"

!include "x64.nsh"
!include "WinVer.nsh"
!include "FileFunc.nsh"

RequestExecutionLevel "${REQUEST_EXECUTION_LEVEL}"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"
VIAddVersionKey "CompanyWebsite"  "https://github.com/Sakana-yuyu/cursor-byok"

ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${INFO_PRODUCTNAME}"
!define MUI_FINISHPAGE_RUN_DEFAULT
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\cursor-byok-windows-386-installer.exe"
InstallDir "$PROGRAMFILES32\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
InstallDirRegKey HKLM "${UNINST_KEY}" "InstallLocation"
SetOverwrite on
ShowInstDetails show

Function .onInit
    # Allow x86 and x64 Windows, reject ARM64
    ${If} ${AtLeastWin10}
        ${If} ${IsNativeARM64}
            IfSilent silentArch notSilentArch
            silentArch:
                SetErrorLevel 65
                Abort
            notSilentArch:
                MessageBox MB_OK "This product can't be installed on ARM64 Windows. Supports: x86, x64"
                Quit
        ${EndIf}
    ${Else}
        IfSilent silentWin notSilentWin
        silentWin:
            SetErrorLevel 64
            Abort
        notSilentWin:
            MessageBox MB_OK "This product is only supported on Windows 10 (Server 2016) and later."
            Quit
    ${EndIf}

    SetRegView 32
    ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
    ${If} $0 != ""
        StrCpy $INSTDIR $0
    ${EndIf}
FunctionEnd

Section
    ${If} "${REQUEST_EXECUTION_LEVEL}" == "admin"
        SetShellVarContext all
    ${Else}
        SetShellVarContext current
    ${EndIf}

    # Close running instance before overwriting
    DetailPrint "Closing ${INFO_PRODUCTNAME} ..."
    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}" /T'
    Pop $0
    Sleep 500

    # Install WebView2 Runtime if not present (32-bit registry view)
    SetRegView 32
    ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${If} $0 != ""
        Goto webview2_ok
    ${EndIf}
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    ${If} $0 != ""
        Goto webview2_ok
    ${EndIf}
    DetailPrint "Installing: WebView2 Runtime"
    InitPluginsDir
    CreateDirectory "$pluginsdir\webview2bootstrapper"
    SetOutPath "$pluginsdir\webview2bootstrapper"
    File "MicrosoftEdgeWebview2Setup.exe"
    ExecWait '"$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe" /silent /install'
    webview2_ok:

    SetOutPath $INSTDIR
    File "/oname=${PRODUCT_EXECUTABLE}" "${ARG_WAILS_386_BINARY}"

    SetOutPath $INSTDIR

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    # Write uninstaller and registry entries (32-bit registry view)
    WriteUninstaller "$INSTDIR\uninstall.exe"
    SetRegView 32
    WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
    WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
    WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
    WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
    WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
    IntFmt $0 "0x%08X" $0
    WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "uninstall"
    ${If} "${REQUEST_EXECUTION_LEVEL}" == "admin"
        SetShellVarContext all
    ${Else}
        SetShellVarContext current
    ${EndIf}

    DetailPrint "Closing ${INFO_PRODUCTNAME} ..."
    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}" /T'
    Pop $0
    Sleep 500

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}"

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    Delete "$INSTDIR\uninstall.exe"
    SetRegView 32
    DeleteRegKey HKLM "${UNINST_KEY}"
SectionEnd