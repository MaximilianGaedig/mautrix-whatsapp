module go.mau.fi/mautrix-whatsapp

go 1.26.0

toolchain go1.27.1

tool go.mau.fi/util/cmd/maubuild

require (
	github.com/lib/pq v1.12.3
	github.com/pion/rtp v1.10.5
	github.com/rs/zerolog v1.35.1
	github.com/tidwall/gjson v1.19.0
	go.mau.fi/util v0.10.1
	go.mau.fi/webp v0.3.0
	go.mau.fi/whatsmeow v0.0.0-20260916100317-2375e1751bbd
	golang.org/x/image v0.46.0
	golang.org/x/net v0.59.0
	golang.org/x/sync v0.23.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/hraban/opus.v2 v2.0.0-20230925203106-0188a62cb302
	gopkg.in/yaml.v3 v3.0.1
	maunium.net/go/mautrix v0.31.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/beeper/argo-go v1.1.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/elliotchance/orderedmap/v3 v3.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.52 // indirect
	github.com/petermattis/goid v0.0.0-20260820044319-269ab09b5261 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/rogpeppe/go-internal v1.10.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/vektah/gqlparser/v2 v2.5.27 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	go.mau.fi/libsignal v0.2.2 // indirect
	go.mau.fi/zeroconfig v0.2.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	maunium.net/go/mauflag v1.0.0 // indirect
)

replace maunium.net/go/mautrix => github.com/MaximilianGaedig/mautrix-go v0.31.1-0.20260924214845-59cd6e4dea34

// Development: the WhatsApp VoIP media library, retargeted at whatsmeow (it is written against a
// fork). Pure Go: it implements SRTP, the RTP WARP framing and Meta's MLow codec without CGO.
replace github.com/purpshell/meowcaller => ../meowcaller

// Development: build against the local whatsmeow carrying upstream PR #1201 (the call
// signalling and media-handoff API). Replaced with a real module path before this ships.
replace go.mau.fi/whatsmeow => ../whatsmeow
