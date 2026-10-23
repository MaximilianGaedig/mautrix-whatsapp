module go.mau.fi/mautrix-whatsapp

go 1.26.0

toolchain go1.27.2

tool go.mau.fi/util/cmd/maubuild

require (
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.12.3
	github.com/pion/ice/v4 v4.4.4
	github.com/pion/rtp v1.10.5
	github.com/pion/webrtc/v4 v4.2.22
	github.com/purpshell/meowcaller v0.0.0-20260906234207-c48c3e2a243c
	github.com/rs/zerolog v1.35.1
	github.com/stretchr/testify v1.12.1
	github.com/tidwall/gjson v1.20.0
	github.com/yuin/goldmark v1.8.6
	go.mau.fi/util v0.11.0
	go.mau.fi/webp v0.3.0
	go.mau.fi/whatsmeow v0.0.0-20261016130820-0b0a1cdac3e2
	golang.org/x/image v0.47.0
	golang.org/x/net v0.61.0
	golang.org/x/sync v0.24.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/hraban/opus.v2 v2.0.0-20230925203106-0188a62cb302
	gopkg.in/yaml.v3 v3.0.1
	maunium.net/go/mautrix v0.32.0
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260415201107-50325440f8f2.1 // indirect
	buf.build/go/protovalidate v1.2.0 // indirect
	buf.build/go/protoyaml v0.7.0 // indirect
	cel.dev/expr v0.25.2 // indirect
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/beeper/argo-go v1.1.2 // indirect
	github.com/benbjohnson/clock v1.3.5 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bep/debounce v1.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/dennwc/iters v1.2.2 // indirect
	github.com/elliotchance/orderedmap/v3 v3.1.0 // indirect
	github.com/frostbyte73/core v0.1.1 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/gammazero/deque v1.2.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/cel-go v0.28.1 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/hajimehoshi/go-mp3 v0.3.4 // indirect
	github.com/jxskiss/base62 v1.1.0 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/lithammer/shortuuid/v4 v4.2.0 // indirect
	github.com/livekit/mageutil v0.0.0-20250511045019-0f1ff63f7731 // indirect
	github.com/livekit/mediatransportutil v0.0.0-20260605212259-862d4a7bcb1e // indirect
	github.com/livekit/protocol v1.49.0 // indirect
	github.com/livekit/psrpc v0.7.2 // indirect
	github.com/livekit/server-sdk-go/v2 v2.18.1 // indirect
	github.com/magefile/mage v1.17.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.52 // indirect
	github.com/petermattis/goid v0.0.0-20260918085751-abfca077860b // indirect
	github.com/rogpeppe/go-internal v1.10.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/twitchtv/twirp v8.1.3+incompatible // indirect
	github.com/vektah/gqlparser/v2 v2.5.27 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.mau.fi/libsignal v0.2.2 // indirect
	go.mau.fi/zeroconfig v0.2.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/exp v0.0.0-20261014143641-f0251eb4aa3e // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	maunium.net/go/mauflag v1.0.0 // indirect
)

replace maunium.net/go/mautrix => github.com/MaximilianGaedig/mautrix-go v0.31.1-0.20261003222753-3011dd5f0133

// Development: the WhatsApp VoIP media library, retargeted at whatsmeow (it is written against a
// fork). Pure Go: it implements SRTP, the RTP WARP framing and Meta's MLow codec without CGO.
replace github.com/purpshell/meowcaller => github.com/MaximilianGaedig/meowcaller v0.0.0-20260929115618-8cc1122354fc

// Development: build against the local whatsmeow carrying upstream PR #1201 (the call
// signalling and media-handoff API). Replaced with a real module path before this ships.
replace go.mau.fi/whatsmeow => github.com/MaximilianGaedig/whatsmeow v0.0.0-20260930131312-468bc79b1e05
