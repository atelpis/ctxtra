package netguard

import "crypto/tls"

func tlsConfig() *tls.Config {
	return &tls.Config{RootCAs: roots(), MinVersion: tls.VersionTLS12}
}
