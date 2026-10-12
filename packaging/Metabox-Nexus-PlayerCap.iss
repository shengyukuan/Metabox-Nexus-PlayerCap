#define AppName "Metabox Nexus PlayerCap"
#define AppVersion "0.0.0"
#define AppPublisher "VTB-TOOLS"
#define CoreExe "Metabox-Nexus-PlayerCap.exe"
#define TrayExe "Metabox-Nexus-PlayerCap-Tray.exe"

[Setup]
AppId={{B8C5B2F2-4F15-4D7F-8A4B-5D2B0CF6E4C1}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={localappdata}\VTB-TOOLS\Metabox-Nexus-PlayerCap
DefaultGroupName={#AppName}
OutputDir=.
OutputBaseFilename=Metabox-Nexus-PlayerCap-Setup
Compression=lzma
SolidCompression=yes
PrivilegesRequired=lowest
UninstallDisplayIcon={app}\{#CoreExe}
WizardStyle=modern

[Files]
Source: "..\dist\{#CoreExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\{#TrayExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\config.yml"; DestDir: "{app}"; Flags: onlyifdoesntexist uninsneveruninstall
Source: "..\dist\lyric_display.html"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\lyric_page.html"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\effect_display.html"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dist\effect_page.html"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#TrayExe}"; IconFilename: "{app}\{#CoreExe}"; IconIndex: 0
Name: "{group}\{#AppName}"; Filename: "{app}\{#TrayExe}"; IconFilename: "{app}\{#CoreExe}"; IconIndex: 0

[Run]
Filename: "{app}\{#TrayExe}"; Parameters: "--install"; WorkingDir: "{app}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\{#TrayExe}"; Parameters: "--uninstall"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated

[UninstallDelete]
Type: files; Name: "{app}\Metabox-Nexus-PlayerCap-Tray.exe"
