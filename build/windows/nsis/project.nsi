Unicode true

####
## Lontar NSIS installer. Built by `wails3 task package:windows`
## (windows:create:nsis:installer), which passes:
##   -DARG_WAILS_AMD64_BINARY=<bin\lontar.exe>   the binary to install
##   -DINFO_PRODUCTVERSION=X.Y.Z                 numeric version (VERSION_NUM)
##   -DLONTAR_VERSION=<full version>             e.g. 1.2.3-rc.1 (VERSION)
##   -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user
##
## Install scope: per-user by default (no UAC prompt), into
## $LOCALAPPDATA\Programs\lontar, with the uninstall entry under HKCU. Build
## with INSTALL_SCOPE=machine for a per-machine install into Program Files.
##
## WebView2: the installer embeds Microsoft's Evergreen *bootstrapper*
## (MicrosoftEdgeWebview2Setup.exe, downloaded by
## `wails3 generate webview2bootstrapper`) and runs it only when no WebView2
## runtime is registered. The bootstrapper downloads the runtime, so that
## step needs internet access (Windows 11 and current Windows 10 already
## ship the runtime).
##
## Identity defaults (company, product, copyright) live in wails_tools.nsh,
## generated from build/config.yml by `wails3 task common:update:build-assets`.
## Defines set before the include below win over those defaults.
####
!include "wails_tools.nsh"

!ifndef LONTAR_VERSION
    !define LONTAR_VERSION "${INFO_PRODUCTVERSION}"
!endif

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${LONTAR_VERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

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

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME} ${LONTAR_VERSION}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR
    
    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols
    
    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall" 
    !insertmacro wails.setShellContext

    # Remove the WebView2 cache (%AppData%\lontar.exe). Lontar's own data
    # (%AppData%\lontar: connections, history, settings) is kept, and saved
    # passwords stay in Windows Credential Manager.
    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}"

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
