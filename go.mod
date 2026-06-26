module github.com/hec-ovi/censurado-web

go 1.26

// The static-site generator reads the published corpus through the backend's
// shared data libraries (domain, store, content, media). The backend repo is
// checked out alongside this one on the same box, so a replace points at it; the
// generator only needs it at build time (the public reading path is fully static).
require github.com/hec-ovi/censurado-web-backend v0.0.0-00010101000000-000000000000

replace github.com/hec-ovi/censurado-web-backend => ../censurado-web-backend

require github.com/santhosh-tekuri/jsonschema/v6 v6.0.2

require (
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/yuin/goldmark v1.8.2 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.53.0 // indirect
)
