package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const (
	// defaultCommandTimeout bounds one remote command. Reading the EEPROM
	// size over i2c is the slowest read inspect does (about a second).
	defaultCommandTimeout = 30 * time.Second
	// defaultMaxOutput bounds one command's stdout; the largest read is a
	// CA bundle or the kernel ring buffer, both well under this.
	defaultMaxOutput = 4 << 20
)

// remoteResult is the outcome of one command that ran to completion.
type remoteResult struct {
	Stdout     []byte
	Stderr     []byte
	ExitStatus int
}

// remoteRunner runs one shell command on the AP. A command that ran and
// exited non-zero is a result, not an error; errors are transport failures.
type remoteRunner interface {
	Run(ctx context.Context, command string) (remoteResult, error)
}

// apSSH is an SSH connection to one AP.
type apSSH struct {
	client         *ssh.Client
	target         sshTarget
	hostKey        ssh.PublicKey
	commandTimeout time.Duration
	maxOutput      int
	closers        []io.Closer
}

// errConnectionLost is returned when the AP closed the session without an
// exit status, which is what a reboot looks like.
var errConnectionLost = errors.New("connection closed before the command reported an exit status")

// dialAP connects and authenticates. Diagnostics (new host keys, skipped
// identities) go to warn.
func dialAP(ctx context.Context, options *sshOptions, target sshTarget, warn io.Writer) (*apSSH, error) {
	files, err := options.knownHostsFiles()
	if err != nil {
		return nil, err
	}
	hosts := &knownHosts{files: files, mode: options.strictMode(), warn: warn}
	signers, closers, err := loadSigners(options, warn)
	if err != nil {
		return nil, err
	}
	closeAll := func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}
	if len(signers) == 0 {
		closeAll()
		return nil, errors.New("no SSH identities: pass -i KEY, load a key into ssh-agent, or create ~/.ssh/id_ed25519 (AP33 0.8 needs an RSA or ECDSA key)")
	}
	hostKeyAlgorithms := options.HostKeyAlgorithms
	if len(hostKeyAlgorithms) == 0 {
		hostKeyAlgorithms = sshDefaultHostKey
	}
	var hostKey ssh.PublicKey
	check := hosts.callback(target.Addr)
	config := &ssh.ClientConfig{
		Config: ssh.Config{
			KeyExchanges: firstNonEmpty(options.KexAlgorithms, sshDefaultKex),
			Ciphers:      firstNonEmpty(options.Ciphers, sshDefaultCiphers),
			MACs:         firstNonEmpty(options.MACs, sshDefaultMACs),
		},
		User: target.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if err := check(hostname, remote, key); err != nil {
				return err
			}
			hostKey = key
			return nil
		},
		HostKeyAlgorithms: hosts.preferredHostKeyAlgorithms(target.Addr, hostKeyAlgorithms),
		Timeout:           options.connectTimeout(),
	}

	dialer := net.Dialer{Timeout: options.connectTimeout()}
	conn, err := dialer.DialContext(ctx, "tcp", target.Addr)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("connect to %s: %w", target.Addr, err)
	}
	if timeout := options.connectTimeout(); timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	clientConn, channels, requests, err := ssh.NewClientConn(conn, target.Addr, config)
	stop()
	if err != nil {
		_ = conn.Close()
		closeAll()
		return nil, explainSSHError(target, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return &apSSH{
		client:         ssh.NewClient(clientConn, channels, requests),
		target:         target,
		hostKey:        hostKey,
		commandTimeout: defaultCommandTimeout,
		maxOutput:      defaultMaxOutput,
		closers:        closers,
	}, nil
}

func firstNonEmpty(values, fallback []string) []string {
	if len(values) > 0 {
		return values
	}
	return fallback
}

func explainSSHError(target sshTarget, err error) error {
	var changed *HostKeyChangedError
	var unknown *HostKeyUnknownError
	if errors.As(err, &changed) || errors.As(err, &unknown) {
		return err
	}
	message := err.Error()
	if strings.Contains(message, "unable to authenticate") || strings.Contains(message, "no common public key signature algorithm") {
		return fmt.Errorf("SSH login as %s@%s failed: %w. Check the key is in the AP's authorized_keys; AP33 0.8 (dropbear 2017.75) accepts only RSA or ECDSA keys, not ed25519", target.User, target.Addr, err)
	}
	if strings.Contains(message, "no common algorithm") {
		return fmt.Errorf("SSH to %s: %w. Adjust the list with -o KexAlgorithms=+..., -o HostKeyAlgorithms=+... or -o Ciphers=+...", target.Addr, err)
	}
	return fmt.Errorf("SSH to %s: %w", target.Addr, err)
}

// loadSigners collects identities in ssh(1) order: explicit -i files, then
// agent keys (unless IdentitiesOnly), then the default key files when no -i
// was given. PubkeyAcceptedAlgorithms then limits the signature algorithms
// each key may use; by default an RSA key may still fall back to SHA-1
// "ssh-rsa" for dropbear 2017.75, which never offers rsa-sha2-*.
func loadSigners(options *sshOptions, warn io.Writer) ([]ssh.Signer, []io.Closer, error) {
	var signers []ssh.Signer
	var closers []io.Closer
	seen := make(map[string]bool)
	add := func(signer ssh.Signer) {
		fingerprint := ssh.FingerprintSHA256(signer.PublicKey())
		if !seen[fingerprint] {
			seen[fingerprint] = true
			signers = append(signers, signer)
		}
	}

	var agentSigners []ssh.Signer
	if socket := agentSocket(options); socket != "" {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			fmt.Fprintf(warn, "warning: ssh-agent at %s unavailable: %v\n", socket, err)
		} else {
			closers = append(closers, conn)
			agentSigners, err = agent.NewClient(conn).Signers()
			if err != nil {
				fmt.Fprintf(warn, "warning: ssh-agent: %v\n", err)
				agentSigners = nil
			}
		}
	}

	loadFile := func(file string, explicit bool) error {
		signer, err := readPrivateKey(file, agentSigners)
		if err == nil {
			add(signer)
			return nil
		}
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if explicit {
			return fmt.Errorf("identity %s: %w", file, err)
		}
		fmt.Fprintf(warn, "warning: skipping identity %s: %v\n", file, err)
		return nil
	}
	for _, file := range options.IdentityFiles {
		if err := loadFile(file, true); err != nil {
			for _, closer := range closers {
				_ = closer.Close()
			}
			return nil, nil, err
		}
	}
	if !options.IdentitiesOnly {
		for _, signer := range agentSigners {
			add(signer)
		}
	}
	if len(options.IdentityFiles) == 0 {
		if home, err := os.UserHomeDir(); err == nil {
			for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
				_ = loadFile(filepath.Join(home, ".ssh", name), false)
			}
		}
	}

	accepted := firstNonEmpty(options.PubkeyAcceptedAlgorithms, sshDefaultPubkey)
	limited := make([]ssh.Signer, 0, len(signers))
	for _, signer := range signers {
		restricted, ok := restrictSigner(signer, accepted)
		if !ok {
			fmt.Fprintf(warn, "warning: skipping %s key %s: no algorithm allowed by PubkeyAcceptedAlgorithms\n",
				signer.PublicKey().Type(), ssh.FingerprintSHA256(signer.PublicKey()))
			continue
		}
		limited = append(limited, restricted)
	}
	return limited, closers, nil
}

func agentSocket(options *sshOptions) string {
	switch options.IdentityAgent {
	case "none":
		return ""
	case "":
		return os.Getenv("SSH_AUTH_SOCK")
	default:
		return options.IdentityAgent
	}
}

// readPrivateKey parses an unencrypted key file. For an encrypted key whose
// public half is known (from the file or file.pub), the matching agent key
// is used instead; dismist never prompts for a passphrase.
func readPrivateKey(file string, agentSigners []ssh.Signer) (ssh.Signer, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, err
	}
	public := missing.PublicKey
	if public == nil {
		if pubData, err := os.ReadFile(file + ".pub"); err == nil {
			public, _, _, _, _ = ssh.ParseAuthorizedKey(pubData)
		}
	}
	if public != nil {
		for _, candidate := range agentSigners {
			if bytes.Equal(candidate.PublicKey().Marshal(), public.Marshal()) {
				return candidate, nil
			}
		}
	}
	return nil, errors.New("key is passphrase-protected; add it to ssh-agent (ssh-add) first")
}

// restrictSigner limits signer to the accepted signature algorithms for its
// key type. It reports false when none is allowed.
func restrictSigner(signer ssh.Signer, accepted []string) (ssh.Signer, bool) {
	var allowed []string
	for _, algorithm := range signatureAlgorithmsForType(signer.PublicKey().Type()) {
		if slices.Contains(accepted, algorithm) {
			allowed = append(allowed, algorithm)
		}
	}
	if len(allowed) == 0 {
		return nil, false
	}
	algorithmSigner, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return signer, slices.Contains(allowed, signer.PublicKey().Type())
	}
	restricted, err := ssh.NewSignerWithAlgorithms(algorithmSigner, allowed)
	if err != nil {
		return nil, false
	}
	return restricted, true
}

func signatureAlgorithmsForType(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

// Close closes the connection and any agent socket.
func (c *apSSH) Close() error {
	err := c.client.Close()
	for _, closer := range c.closers {
		_ = closer.Close()
	}
	return err
}

// HostKeyFingerprint is the SHA256 fingerprint of the AP's host key.
func (c *apSSH) HostKeyFingerprint() string {
	if c.hostKey == nil {
		return ""
	}
	return ssh.FingerprintSHA256(c.hostKey)
}

// Run executes command through the AP's shell, bounded by the command
// timeout and the output limit.
func (c *apSSH) Run(ctx context.Context, command string) (remoteResult, error) {
	if c.commandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.commandTimeout)
		defer cancel()
	}
	session, err := c.client.NewSession()
	if err != nil {
		return remoteResult{}, fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()
	stdout := &limitedBuffer{limit: c.maxOutput, onExceed: func() { _ = session.Close() }}
	stderr := &limitedBuffer{limit: 64 << 10, onExceed: func() { _ = session.Close() }}
	session.Stdout = stdout
	session.Stderr = stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = session.Close()
		return remoteResult{}, fmt.Errorf("%q: %w", command, ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return remoteResult{}, fmt.Errorf("%q: output exceeds the %d byte limit", command, c.maxOutput)
	}
	result := remoteResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		result.ExitStatus = exit.ExitStatus()
		return result, nil
	}
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) || errors.Is(err, io.EOF) {
		return result, errConnectionLost
	}
	return result, fmt.Errorf("%q: %w", command, err)
}

// limitedBuffer fails writes past its limit and calls onExceed (which closes
// the session), so a runaway command cannot exhaust memory or hang. It must
// not embed bytes.Buffer: io.Copy would use the promoted ReadFrom and bypass
// the limit.
type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
	onExceed func()
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.buf.Len()+len(data) > b.limit {
		if !b.exceeded && b.onExceed != nil {
			b.onExceed()
		}
		b.exceeded = true
		return 0, fmt.Errorf("command output exceeds %d bytes", b.limit)
	}
	return b.buf.Write(data)
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

// shellQuote quotes value for a POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
