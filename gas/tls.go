package main

import "crypto/tls"

func defaultTLSConfig(insecure bool) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: insecure,
	}
}