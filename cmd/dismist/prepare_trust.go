package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const rwRootCertsPath = "/rw/etc/mist/ep-control/cacerts-production.pem"

var (
	cloudURLSetting     = regexp.MustCompile(`(?m)^[ \t]*url[ \t]*=[ \t]*"([^"]+)"`)
	rootCertsSetting    = regexp.MustCompile(`(?m)^([ \t]*rootcerts[ \t]*=[ \t]*)"([^"]+)"`)
	insecureTrueSetting = regexp.MustCompile(`(?m)^[ \t]*insecure[ \t]*=[ \t]*true\b`)
)

func runPrepareTrust(args []string, output io.Writer) error {
	const usage = "usage: dismist ap33 prepare-trust --stock-dir DIR --controller-ca FILE [--controller-ca FILE...] [--url wss://HOST/ws] --output DIR"
	flags := flag.NewFlagSet("dismist ap33 prepare-trust", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stockDir := flags.String("stock-dir", "", "stock firmware root directory")
	var controllerCAs []string
	flags.Func("controller-ca", "PEM file of CA certificates to add: the local data/ca.crt, or the public roots that issue the served chain (repeatable)", func(value string) error {
		if value == "" {
			return errors.New("empty CA file")
		}
		controllerCAs = append(controllerCAs, value)
		return nil
	})
	endpoint := flags.String("url", "", "controller WSS URL for both [[cloud]] url keys (default: keep the stock ep-terminator name, for DNS mode)")
	outputDir := flags.String("output", "", "private overlay output directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", usage, err)
	}
	if *stockDir == "" || len(controllerCAs) == 0 || *outputDir == "" || flags.NArg() != 0 {
		return errors.New(usage)
	}
	if err := prepareTrustOverlay(*stockDir, controllerCAs, *endpoint, *outputDir); err != nil {
		return fmt.Errorf("/rw overlay preparation failed: %w", err)
	}
	_, err := fmt.Fprintf(output, "private /rw overlay prepared at %s\n", *outputDir)
	return err
}

// prepareTrustOverlay builds /rw/etc/mist/ep-control/{cloud.ini,
// cacerts-production.pem} from the AP's stock files: the stock bundle plus
// the extra CAs, every rootcerts path pointed at the /rw bundle and, when
// endpoint is set, every [[cloud]] url replaced by it.
func prepareTrustOverlay(stockDir string, controllerCAs []string, endpoint, outputDir string) error {
	if endpoint != "" {
		if err := checkCloudURL(endpoint); err != nil {
			return err
		}
	}
	source := filepath.Join(stockDir, "etc", "mist", "ep-control")
	config, err := os.ReadFile(filepath.Join(source, "cloud.ini"))
	if err != nil {
		return err
	}
	stockCA, err := os.ReadFile(filepath.Join(source, "cacerts-production.pem"))
	if err != nil {
		return err
	}
	if err := checkCABundle(stockCA); err != nil {
		return err
	}
	var localCA []byte
	for _, file := range controllerCAs {
		extra, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := checkCABundle(extra); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		localCA = append(localCA, bytes.TrimSpace(extra)...)
		localCA = append(localCA, '\n')
	}

	urls := cloudURLSetting.FindAllSubmatch(config, -1)
	if len(urls) == 0 {
		return errors.New("unexpected stock cloud endpoint")
	}
	for _, match := range urls {
		stockURL := string(match[1])
		if stockURL != "wss://ep-terminator.mistsys.net/ws" && stockURL != "wss://ep-terminator${FIPS}.mistsys.net/ws" {
			return errors.New("unexpected stock cloud endpoint")
		}
	}
	if insecureTrueSetting.Match(config) {
		return errors.New("stock config disables TLS verification")
	}
	localConfig, err := configWithRWRootCerts(string(config))
	if err != nil {
		return err
	}
	if endpoint != "" {
		localConfig = cloudURLSetting.ReplaceAllStringFunc(localConfig, func(setting string) string {
			match := cloudURLSetting.FindStringSubmatchIndex(setting)
			return setting[:match[2]] + endpoint + setting[match[3]:]
		})
	}
	bundle := make([]byte, 0, len(stockCA)+len(localCA)+2)
	bundle = append(bundle, bytes.TrimRight(stockCA, " \t\r\n")...)
	bundle = append(bundle, '\n')
	bundle = append(bundle, bytes.TrimSpace(localCA)...)
	bundle = append(bundle, '\n')
	if err := checkCABundle(bundle); err != nil {
		return err
	}

	if err := os.Mkdir(outputDir, 0o700); err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(outputDir)
		}
	}()
	root := filepath.Join(outputDir, "etc", "mist", "ep-control")
	for _, dir := range []string{outputDir, filepath.Join(outputDir, "etc"), filepath.Join(outputDir, "etc", "mist"), root} {
		if dir != outputDir {
			if err := os.Mkdir(dir, 0o700); err != nil {
				return err
			}
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	if err := writePrivateFile(filepath.Join(root, "cloud.ini"), []byte(localConfig)); err != nil {
		return err
	}
	if err := writePrivateFile(filepath.Join(root, "cacerts-production.pem"), bundle); err != nil {
		return err
	}
	success = true
	return nil
}

// checkCloudURL accepts the WSS endpoint an AP can use: wss scheme, a host,
// no credentials, query or fragment, and nothing that needs TOML escaping.
func checkCloudURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "wss" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, "\"\\ \t\r\n") {
		return fmt.Errorf("invalid controller URL %q: use wss://HOST[:PORT]/ws", raw)
	}
	return nil
}

func checkCABundle(data []byte) error {
	begin := bytes.Count(data, []byte("-----BEGIN CERTIFICATE-----"))
	end := bytes.Count(data, []byte("-----END CERTIFICATE-----"))
	if begin == 0 || begin != end {
		return errors.New("invalid PEM CA bundle")
	}
	return nil
}

func configWithRWRootCerts(config string) (string, error) {
	matches := rootCertsSetting.FindAllStringSubmatchIndex(config, -1)
	if len(matches) == 0 {
		return "", errors.New("stock cloud config has no rootcerts setting")
	}
	var result strings.Builder
	last := 0
	for _, match := range matches {
		old := config[match[4]:match[5]]
		if old != rwRootCertsPath && !strings.HasPrefix(old, "/etc/mist/ep-control/cacerts-production") {
			return "", errors.New("unexpected stock root CA path")
		}
		result.WriteString(config[last:match[4]])
		result.WriteString(rwRootCertsPath)
		last = match[5]
	}
	result.WriteString(config[last:])
	return result.String(), nil
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
