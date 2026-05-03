module go_module

go 1.26

replace github.com/cbeuw/Cloak => ./modules/Cloak

replace github.com/xjasonlyu/tun2socks/v2/log => ./go_module/log

require (
	github.com/Jigsaw-Code/outline-sdk v0.0.20
	github.com/Jigsaw-Code/outline-sdk/x v0.0.8
	github.com/amnezia-vpn/amneziawg-go v0.2.16
	github.com/amnezia-vpn/amneziawg-windows v0.1.8
	github.com/cbeuw/Cloak v0.0.0-00010101000000-000000000000
	github.com/hyperion-cs/dpi-checkers/ru/dpi-ch v0.0.0-20260402150012-367d1de066bd
	github.com/jackpal/gateway v1.1.1
	github.com/lorenzosaino/go-sysctl v0.3.1
	github.com/matsuridayo/libneko v0.0.0-20240702024904-1c47a3af7199
	github.com/sirupsen/logrus v1.9.3
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/things-go/go-socks5 v0.1.0
	github.com/vishvananda/netlink v1.2.1-beta.2
	github.com/xjasonlyu/tun2socks/v2 v2.6.0
	go.opentelemetry.io/contrib/bridges/otelslog v0.18.0
	go.opentelemetry.io/otel v1.43.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.19.0
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.43.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.43.0
	go.opentelemetry.io/otel/log v0.19.0
	go.opentelemetry.io/otel/sdk v1.43.0
	go.opentelemetry.io/otel/sdk/log v0.19.0
	go.opentelemetry.io/otel/sdk/metric v1.43.0
	golang.org/x/crypto v0.49.0
	golang.org/x/sys v0.42.0
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2
	golang.zx2c4.com/wireguard/windows v0.5.3
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/ajg/form v1.5.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/go-chi/chi/v5 v5.2.1 // indirect
	github.com/go-chi/cors v1.2.1 // indirect
	github.com/go-chi/render v1.0.3 // indirect
	github.com/go-gost/relay v0.5.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/schema v1.4.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.zx2c4.com/wireguard v0.0.0-20250521234502-f333402bd9cb // indirect
)

require (
	golang.org/x/text v0.35.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260401024825-9d38bb4040a9 // indirect
)

require (
	code.gitea.io/sdk/gitea v0.22.1 // indirect
	github.com/42wim/httpsig v1.2.3 // indirect
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creativeprojects/go-selfupdate v1.5.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/expr-lang/expr v1.17.8 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-fed/httpsig v1.1.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/go-github/v74 v74.0.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/go-version v1.8.0 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/pelletier/go-toml/v2 v2.3.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/shadowsocks/go-shadowsocks2 v0.1.5 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	gitlab.com/gitlab-org/api/client-go v1.9.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616 // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	golang.org/x/tools v0.42.0 // indirect
	golang.org/x/tools/go/expect v0.1.1-deprecated // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260401024825-9d38bb4040a9 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gvisor.dev/gvisor v0.0.0-20250523182742-eede7a881b20 // indirect
	honnef.co/go/tools v0.5.1 // indirect
)
