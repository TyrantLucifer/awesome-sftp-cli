module github.com/TyrantLucifer/awesome-mac-sftp

go 1.25.0

toolchain go1.26.5

require (
	github.com/gdamore/tcell/v3 v3.4.0
	github.com/pkg/sftp v1.13.11
)

replace github.com/pkg/sftp => github.com/TyrantLucifer/sftp v1.13.12-0.20260715132526-f947b886400b

require (
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)
