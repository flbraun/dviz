package app

import (
	"net"
	"os/exec"
)

// xdgOpen opens url with xdg-open without waiting for the browser.
func xdgOpen(url string) error {
	cmd := exec.Command("xdg-open", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// browserURL turns a bound listener address into a URL a local browser can open:
// wildcard addresses are replaced by localhost.
func browserURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}
