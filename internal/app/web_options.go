package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
)

type webOptions struct {
	workspace string
	noOpen    bool
	listen    string
	tlsCert   string
	tlsKey    string
	tokenFile string
}

func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func parseWebOptions(args []string, stdout io.Writer) (webOptions, bool, error) {
	var options webOptions
	flags := flag.NewFlagSet("lune web", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.workspace, "workspace", "", "workspace directory")
	flags.BoolVar(&options.noOpen, "no-open", false, "print the launch link instead of opening the browser")
	flags.StringVar(&options.listen, "listen", "127.0.0.1:0", "listen address for Web host")
	flags.StringVar(&options.tlsCert, "tls-cert", "", "TLS certificate PEM file")
	flags.StringVar(&options.tlsKey, "tls-key", "", "TLS private key PEM file")
	flags.StringVar(&options.tokenFile, "token-file", "", "file containing the access token for HTTPS Web access")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: lune web [--workspace DIRECTORY] [--no-open] [--listen ADDR --tls-cert PEM --tls-key PEM --token-file PATH]")
			return options, false, nil
		}
		return options, false, err
	}
	if flags.NArg() != 0 {
		return options, false, errors.New("usage: lune web [--workspace DIRECTORY] [--no-open] [--listen ADDR --tls-cert PEM --tls-key PEM --token-file PATH]")
	}
	if (options.tlsCert == "") != (options.tlsKey == "") {
		return options, false, errors.New("lune web requires both --tls-cert and --tls-key")
	}
	if !isLoopbackListen(options.listen) {
		if options.tlsCert == "" || options.tlsKey == "" || options.tokenFile == "" {
			return options, false, errors.New("non-loopback --listen requires --tls-cert, --tls-key, and --token-file")
		}
	}
	return options, true, nil
}
