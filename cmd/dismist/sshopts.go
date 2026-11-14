package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// The AP33's dropbear differs by firmware. The 0.8 recovery image runs
// dropbear 2017.75: no rsa-sha2-* signatures and no server-sig-algs, so an
// RSA user key must sign with SHA-1 "ssh-rsa", and its RSA host key (when it
// has one) is offered as "ssh-rsa" only. 0.14/0.15 run dropbear 2022.83,
// which negotiates rsa-sha2-256 and curve25519. dismist's defaults are the
// x/crypto secure set plus exactly those legacy algorithms, so both work
// without extra flags; -o can still narrow or extend each list.
var (
	sshDefaultKex = appendUnique(ssh.SupportedAlgorithms().KeyExchanges,
		ssh.InsecureKeyExchangeDH14SHA1)
	sshDefaultCiphers = ssh.SupportedAlgorithms().Ciphers
	sshDefaultMACs    = ssh.SupportedAlgorithms().MACs
	sshDefaultHostKey = appendUnique(ssh.SupportedAlgorithms().HostKeys,
		ssh.KeyAlgoRSA)
	sshDefaultPubkey = appendUnique(ssh.SupportedAlgorithms().PublicKeyAuths,
		ssh.KeyAlgoRSA)
)

const (
	defaultSSHUser           = "root"
	defaultSSHPort           = 22
	defaultConnectTimeout    = 10 * time.Second
	defaultStrictHostKeyMode = "accept-new"
)

// sshOptions is the subset of ssh_config(5) that dismist honours. Values come
// from -o Key=Value flags and the dedicated short flags first, then from an
// optional -F file; as in ssh(1) the first value obtained for a keyword wins,
// except IdentityFile, which accumulates.
type sshOptions struct {
	User           string
	HostName       string
	Port           int
	IdentityFiles  []string
	IdentitiesOnly bool
	// IdentityAgent is a socket path; "" means $SSH_AUTH_SOCK and "none"
	// disables the agent.
	IdentityAgent         string
	KnownHostsFiles       []string
	StrictHostKeyChecking string
	ConnectTimeout        time.Duration

	HostKeyAlgorithms        []string
	PubkeyAcceptedAlgorithms []string
	KexAlgorithms            []string
	Ciphers                  []string
	MACs                     []string

	set map[string]bool
}

func newSSHOptions() *sshOptions {
	return &sshOptions{set: make(map[string]bool)}
}

// sshKeyword is one supported ssh_config keyword.
type sshKeyword struct {
	name  string
	apply func(o *sshOptions, value string) error
}

var sshKeywords = func() map[string]sshKeyword {
	list := []sshKeyword{
		{"User", func(o *sshOptions, v string) error {
			if v == "" {
				return errors.New("empty user")
			}
			o.User = v
			return nil
		}},
		{"HostName", func(o *sshOptions, v string) error {
			if v == "" {
				return errors.New("empty host name")
			}
			o.HostName = v
			return nil
		}},
		{"Port", func(o *sshOptions, v string) error {
			port, err := strconv.Atoi(v)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("bad port %q", v)
			}
			o.Port = port
			return nil
		}},
		{"IdentityFile", func(o *sshOptions, v string) error {
			if v == "" {
				return errors.New("empty identity file")
			}
			o.IdentityFiles = append(o.IdentityFiles, expandHome(v))
			return nil
		}},
		{"IdentitiesOnly", func(o *sshOptions, v string) error {
			b, err := parseSSHBool(v)
			o.IdentitiesOnly = b
			return err
		}},
		{"IdentityAgent", func(o *sshOptions, v string) error {
			switch {
			case strings.EqualFold(v, "none"):
				o.IdentityAgent = "none"
			case v == "SSH_AUTH_SOCK" || v == "$SSH_AUTH_SOCK":
				o.IdentityAgent = ""
			default:
				o.IdentityAgent = expandHome(v)
			}
			return nil
		}},
		{"UserKnownHostsFile", func(o *sshOptions, v string) error {
			files := strings.Fields(v)
			if len(files) == 0 {
				return errors.New("empty known hosts file")
			}
			o.KnownHostsFiles = nil
			for _, file := range files {
				o.KnownHostsFiles = append(o.KnownHostsFiles, expandHome(file))
			}
			return nil
		}},
		{"StrictHostKeyChecking", func(o *sshOptions, v string) error {
			switch strings.ToLower(v) {
			case "yes", "true", "ask":
				// dismist never prompts; "ask" behaves like "yes".
				o.StrictHostKeyChecking = "yes"
			case "accept-new":
				o.StrictHostKeyChecking = "accept-new"
			case "no", "off", "false":
				o.StrictHostKeyChecking = "no"
			default:
				return fmt.Errorf("bad value %q (use yes, accept-new or no)", v)
			}
			return nil
		}},
		{"ConnectTimeout", func(o *sshOptions, v string) error {
			seconds, err := strconv.Atoi(v)
			if err != nil || seconds < 0 {
				return fmt.Errorf("bad timeout %q (seconds)", v)
			}
			o.ConnectTimeout = time.Duration(seconds) * time.Second
			return nil
		}},
		{"BatchMode", func(o *sshOptions, v string) error {
			// dismist never prompts, so BatchMode is always in effect.
			_, err := parseSSHBool(v)
			return err
		}},
		{"HostKeyAlgorithms", algorithmKeyword(func(o *sshOptions) *[]string { return &o.HostKeyAlgorithms }, sshDefaultHostKey, sshAllHostKeyAlgorithms())},
		{"PubkeyAcceptedAlgorithms", algorithmKeyword(func(o *sshOptions) *[]string { return &o.PubkeyAcceptedAlgorithms }, sshDefaultPubkey, sshAllPubkeyAlgorithms())},
		{"KexAlgorithms", algorithmKeyword(func(o *sshOptions) *[]string { return &o.KexAlgorithms }, sshDefaultKex, sshAllKexAlgorithms())},
		{"Ciphers", algorithmKeyword(func(o *sshOptions) *[]string { return &o.Ciphers }, sshDefaultCiphers, sshAllCiphers())},
		{"MACs", algorithmKeyword(func(o *sshOptions) *[]string { return &o.MACs }, sshDefaultMACs, sshAllMACs())},
	}
	keywords := make(map[string]sshKeyword, len(list)+1)
	for _, keyword := range list {
		keywords[strings.ToLower(keyword.name)] = keyword
	}
	// The pre-8.5 OpenSSH name for PubkeyAcceptedAlgorithms.
	keywords["pubkeyacceptedkeytypes"] = keywords["pubkeyacceptedalgorithms"]
	return keywords
}()

func supportedSSHKeywords() string {
	names := make([]string, 0, len(sshKeywords))
	for key, keyword := range sshKeywords {
		if key == "pubkeyacceptedkeytypes" {
			continue
		}
		names = append(names, keyword.name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// setOption applies one ssh_config keyword. The first value for a keyword
// wins; IdentityFile accumulates.
func (o *sshOptions) setOption(key, value string) error {
	keyword, ok := sshKeywords[strings.ToLower(key)]
	if !ok {
		return fmt.Errorf("unsupported SSH option %q (supported: %s)", key, supportedSSHKeywords())
	}
	canonical := strings.ToLower(keyword.name)
	if canonical != "identityfile" && o.set[canonical] {
		return nil
	}
	if err := keyword.apply(o, unquoteSSHValue(value)); err != nil {
		return fmt.Errorf("SSH option %s: %w", keyword.name, err)
	}
	o.set[canonical] = true
	return nil
}

// setOptionString parses "Key=Value" or "Key Value", as -o accepts.
func (o *sshOptions) setOptionString(option string) error {
	key, value, ok := splitSSHOption(option)
	if !ok {
		return fmt.Errorf("bad SSH option %q (use Key=Value)", option)
	}
	return o.setOption(key, value)
}

func splitSSHOption(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	index := strings.IndexAny(line, "= \t")
	if index <= 0 {
		return "", "", false
	}
	key = line[:index]
	value = strings.TrimLeft(line[index:], " \t")
	if strings.HasPrefix(value, "=") {
		value = strings.TrimLeft(value[1:], " \t")
	}
	return key, strings.TrimSpace(value), true
}

func unquoteSSHValue(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}

func parseSSHBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "yes", "true", "on":
		return true, nil
	case "no", "false", "off":
		return false, nil
	}
	return false, fmt.Errorf("bad boolean %q (use yes or no)", value)
}

// algorithmKeyword resolves an OpenSSH algorithm list: "a,b" replaces the
// defaults, "+a,b" appends, "-a,b" removes (wildcards allowed) and "^a,b"
// puts the listed algorithms first.
func algorithmKeyword(field func(*sshOptions) *[]string, defaults, known []string) func(*sshOptions, string) error {
	return func(o *sshOptions, value string) error {
		resolved, err := resolveAlgorithms(value, defaults, known)
		if err != nil {
			return err
		}
		*field(o) = resolved
		return nil
	}
}

func resolveAlgorithms(value string, defaults, known []string) ([]string, error) {
	if value == "" {
		return nil, errors.New("empty algorithm list")
	}
	mode := byte(0)
	if strings.ContainsRune("+-^", rune(value[0])) {
		mode = value[0]
		value = value[1:]
	}
	var names []string
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("empty algorithm name")
		}
		if mode != '-' && !slices.Contains(known, name) {
			return nil, fmt.Errorf("unknown or unsupported algorithm %q", name)
		}
		names = append(names, name)
	}
	var result []string
	switch mode {
	case '+':
		result = appendUnique(slices.Clone(defaults), names...)
	case '^':
		result = appendUnique(slices.Clone(names), defaults...)
	case '-':
		for _, algorithm := range defaults {
			if !matchesAnyPattern(algorithm, names) {
				result = append(result, algorithm)
			}
		}
	default:
		result = appendUnique(nil, names...)
	}
	if len(result) == 0 {
		return nil, errors.New("algorithm list is empty after applying it")
	}
	return result, nil
}

func matchesAnyPattern(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if wildcardMatch(pattern, value) {
			return true
		}
	}
	return false
}

// wildcardMatch matches ssh_config patterns, where only "*" and "?" are
// special (unlike path.Match, "[" is literal: known_hosts uses "[host]:port").
func wildcardMatch(pattern, value string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if pattern == "" {
				return true
			}
			for i := 0; i <= len(value); i++ {
				if wildcardMatch(pattern, value[i:]) {
					return true
				}
			}
			return false
		case '?':
			if value == "" {
				return false
			}
		default:
			if value == "" || pattern[0] != value[0] {
				return false
			}
		}
		pattern, value = pattern[1:], value[1:]
	}
	return value == ""
}

func appendUnique(list []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(list, value) {
			list = append(list, value)
		}
	}
	return list
}

func sshAllKexAlgorithms() []string {
	return appendUnique(ssh.SupportedAlgorithms().KeyExchanges, ssh.InsecureAlgorithms().KeyExchanges...)
}

func sshAllCiphers() []string {
	return appendUnique(ssh.SupportedAlgorithms().Ciphers, ssh.InsecureAlgorithms().Ciphers...)
}

func sshAllMACs() []string {
	return appendUnique(ssh.SupportedAlgorithms().MACs, ssh.InsecureAlgorithms().MACs...)
}

func sshAllHostKeyAlgorithms() []string {
	return appendUnique(ssh.SupportedAlgorithms().HostKeys, ssh.InsecureAlgorithms().HostKeys...)
}

func sshAllPubkeyAlgorithms() []string {
	return appendUnique(ssh.SupportedAlgorithms().PublicKeyAuths, ssh.InsecureAlgorithms().PublicKeyAuths...)
}

// readSSHConfig applies the parts of an ssh_config(5) file that match host:
// global lines and Host blocks whose patterns match. Match blocks and
// keywords dismist does not implement are skipped with a warning, because a
// user's own config often carries options that only matter to OpenSSH.
func (o *sshOptions) readSSHConfig(file, host string, warn io.Writer) error {
	handle, err := os.Open(expandHome(file))
	if err != nil {
		return err
	}
	defer handle.Close()
	scanner := bufio.NewScanner(handle)
	active := true
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := splitSSHOption(line)
		if !ok {
			return fmt.Errorf("%s:%d: bad line", file, lineNumber)
		}
		switch strings.ToLower(key) {
		case "host":
			active = hostPatternsMatch(strings.Fields(value), host)
			continue
		case "match":
			active = false
			fmt.Fprintf(warn, "warning: %s:%d: Match blocks are not supported; skipping the block\n", file, lineNumber)
			continue
		}
		if !active {
			continue
		}
		if _, known := sshKeywords[strings.ToLower(key)]; !known {
			fmt.Fprintf(warn, "warning: %s:%d: ignoring unsupported option %s\n", file, lineNumber, key)
			continue
		}
		if err := o.setOption(key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", file, lineNumber, err)
		}
	}
	return scanner.Err()
}

// hostPatternsMatch follows ssh_config Host matching: any matching negated
// pattern excludes the host, otherwise any matching pattern includes it.
func hostPatternsMatch(patterns []string, host string) bool {
	matched := false
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		if !wildcardMatch(strings.ToLower(pattern), strings.ToLower(host)) {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

func expandHome(file string) string {
	if file == "~" || strings.HasPrefix(file, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(file, "~"))
		}
	}
	return file
}

// sshTarget is a resolved destination.
type sshTarget struct {
	User string
	Host string // as given on the command line (the ssh_config alias)
	Addr string // host:port actually dialled
}

func (t sshTarget) String() string {
	return t.User + "@" + t.Addr
}

// parseDestination accepts [user@]host and ssh://[user@]host[:port], with
// IPv6 literals in brackets.
func parseDestination(destination string) (user, host string, port int, err error) {
	if destination == "" {
		return "", "", 0, errors.New("missing AP destination ([user@]host)")
	}
	if strings.HasPrefix(destination, "ssh://") {
		parsed, err := url.Parse(destination)
		if err != nil || parsed.Hostname() == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" {
			return "", "", 0, fmt.Errorf("bad destination %q", destination)
		}
		if parsed.User != nil {
			user = parsed.User.Username()
		}
		if parsed.Port() != "" {
			port, err = strconv.Atoi(parsed.Port())
			if err != nil || port < 1 || port > 65535 {
				return "", "", 0, fmt.Errorf("bad port in %q", destination)
			}
		}
		return user, parsed.Hostname(), port, nil
	}
	if index := strings.LastIndex(destination, "@"); index >= 0 {
		user, destination = destination[:index], destination[index+1:]
		if user == "" {
			return "", "", 0, errors.New("empty user in destination")
		}
	}
	if strings.HasPrefix(destination, "[") && strings.HasSuffix(destination, "]") {
		destination = destination[1 : len(destination)-1]
	}
	if destination == "" || strings.ContainsAny(destination, " \t/[]") {
		return "", "", 0, fmt.Errorf("bad AP host %q", destination)
	}
	if strings.Contains(destination, ":") && net.ParseIP(destination) == nil {
		return "", "", 0, fmt.Errorf("bad AP host %q (use -p or ssh://host:port for a port)", destination)
	}
	return user, destination, 0, nil
}

// resolve combines the destination with the options. The destination's user
// and port beat -l/-p and -o, which beat the -F file; the AP33 only has root.
func (o *sshOptions) resolve(destination string) (sshTarget, error) {
	user, host, port, err := parseDestination(destination)
	if err != nil {
		return sshTarget{}, err
	}
	if user == "" {
		user = o.User
	}
	if user == "" {
		user = defaultSSHUser
	}
	if port == 0 {
		port = o.Port
	}
	if port == 0 {
		port = defaultSSHPort
	}
	dialHost := host
	if o.HostName != "" {
		dialHost = o.HostName
	}
	return sshTarget{User: user, Host: host, Addr: net.JoinHostPort(dialHost, strconv.Itoa(port))}, nil
}

func (o *sshOptions) connectTimeout() time.Duration {
	if o.set["connecttimeout"] {
		return o.ConnectTimeout
	}
	return defaultConnectTimeout
}

func (o *sshOptions) strictMode() string {
	if o.StrictHostKeyChecking == "" {
		return defaultStrictHostKeyMode
	}
	return o.StrictHostKeyChecking
}

func (o *sshOptions) knownHostsFiles() ([]string, error) {
	if len(o.KnownHostsFiles) > 0 {
		return o.KnownHostsFiles, nil
	}
	file, err := defaultKnownHostsFile()
	if err != nil {
		return nil, err
	}
	return []string{file}, nil
}

// defaultKnownHostsFile keeps AP host keys apart from ~/.ssh/known_hosts:
// APs regenerate their host key on a factory reset and LAN addresses get
// reused, which would otherwise trip OpenSSH's changed-key warning for
// unrelated hosts. -o UserKnownHostsFile=~/.ssh/known_hosts shares it.
func defaultKnownHostsFile() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "dismist", "known_hosts"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the home directory for known_hosts: %w", err)
	}
	return filepath.Join(home, ".config", "dismist", "known_hosts"), nil
}

// sshFlags registers the shared SSH flags on a command's FlagSet. Options are
// applied in order after parsing (see apply), so -o and the short flags are
// "first value wins" exactly like ssh(1).
type sshFlags struct {
	options    []string
	identities []string
	port       int
	login      string
	knownHosts string
	configFile string
}

func (f *sshFlags) register(flags *flag.FlagSet) {
	flags.Func("o", "SSH option in ssh_config form, e.g. -o StrictHostKeyChecking=yes (repeatable)", func(value string) error {
		f.options = append(f.options, value)
		return nil
	})
	identity := func(value string) error {
		f.identities = append(f.identities, value)
		return nil
	}
	flags.Func("i", "SSH private key file (repeatable)", identity)
	flags.Func("identity", "same as -i", identity)
	flags.IntVar(&f.port, "p", 0, "SSH port (default 22)")
	flags.StringVar(&f.login, "l", "", "SSH user (default root)")
	flags.StringVar(&f.knownHosts, "known-hosts", "", "known_hosts file (default ~/.config/dismist/known_hosts)")
	flags.StringVar(&f.configFile, "F", "", "read Host blocks from this ssh_config file (not read by default)")
}

// apply builds the options for destination. Command-line values come first
// and win; the -F file fills in the rest.
func (f *sshFlags) apply(destination string, warn io.Writer) (*sshOptions, sshTarget, error) {
	options := newSSHOptions()
	for _, identity := range f.identities {
		if err := options.setOption("IdentityFile", identity); err != nil {
			return nil, sshTarget{}, err
		}
	}
	if f.port != 0 {
		if err := options.setOption("Port", strconv.Itoa(f.port)); err != nil {
			return nil, sshTarget{}, err
		}
	}
	if f.login != "" {
		if err := options.setOption("User", f.login); err != nil {
			return nil, sshTarget{}, err
		}
	}
	if f.knownHosts != "" {
		if err := options.setOption("UserKnownHostsFile", f.knownHosts); err != nil {
			return nil, sshTarget{}, err
		}
	}
	for _, option := range f.options {
		if err := options.setOptionString(option); err != nil {
			return nil, sshTarget{}, err
		}
	}
	if f.configFile != "" {
		_, host, _, err := parseDestination(destination)
		if err != nil {
			return nil, sshTarget{}, err
		}
		if err := options.readSSHConfig(f.configFile, host, warn); err != nil {
			return nil, sshTarget{}, err
		}
	}
	target, err := options.resolve(destination)
	if err != nil {
		return nil, sshTarget{}, err
	}
	return options, target, nil
}

// parseInterspersed parses flags that may appear before or after positional
// arguments ("inspect root@ap --json" and "inspect --json root@ap").
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		rest := flags.Args()
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}
