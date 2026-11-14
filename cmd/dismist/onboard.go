package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
)

const onboardUsage = `usage: dismist ap33 onboard [ssh flags] [user@]host --controller URL --token-file FILE
         --name NAME [--site NAME|ID (default Home)] [--pin-ip] [--backup-dir DIR] [--no-reboot]
         [--remove-local-files] [--dry-run] [--yes] [--ca FILE]...
         [--public-roots auto|letsencrypt|none] [--timeout 5m]
   or: dismist ap33 onboard --retarget [ssh flags] [user@]host --controller URL --token-file FILE [...]`

const (
	defaultOnboardSite   = "Home"
	onboardRebootReason  = "dismist ap33 onboard"
	defaultOnboardWait   = 5 * time.Minute
	defaultPollInterval  = 5 * time.Second
	servedChainTimeout   = 15 * time.Second
	rwCloudINIPath       = apEPControlRW + "/cloud.ini"
	stockCloudINIPath    = apEPControlRO + "/cloud.ini"
	stockBundlePath      = apEPControlRO + "/cacerts-production.pem"
	localFilesAsideRoot  = "/rw/etc/mist/dismist-local-files"
	publicRootsAuto      = "auto"
	publicRootsLE        = "letsencrypt"
	publicRootsNone      = "none"
	onboardConfirmReboot = "The AP is connected to this controller now. Reboot it so it reads the new files? Its clients lose Wi-Fi for about two minutes."
)

type onboardOptions struct {
	controller  string
	tokenFile   string
	site        string
	name        string
	backupDir   string
	publicRoots string
	extraCAs    []string
	pinIP       bool
	noReboot    bool
	removeLocal bool
	dryRun      bool
	yes         bool
	retarget    bool
	timeout     time.Duration
}

// runOnboard is `dismist ap33 onboard` (design §3.15): back up the AP,
// read its secret, install the controller's cloud.ini and CA bundle,
// register it through the API, reboot it and wait until it is in sync.
// Everything that can fail without touching the AP is checked first.
func runOnboard(args []string, env ap33Env) error {
	flags := flag.NewFlagSet("dismist ap33 onboard", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	var ssh sshFlags
	ssh.register(flags)
	var options onboardOptions
	flags.StringVar(&options.controller, "controller", "", "controller URL, e.g. https://dismist.example.net")
	flags.StringVar(&options.tokenFile, "token-file", "", "file holding an API token (admin scope)")
	flags.StringVar(&options.site, "site", defaultOnboardSite, "site name or ID to register the AP in")
	flags.StringVar(&options.name, "name", "", "AP name")
	flags.StringVar(&options.backupDir, "backup-dir", "", "backup directory (default ~/.local/share/dismist/backups)")
	flags.StringVar(&options.publicRoots, "public-roots", publicRootsAuto, "add Let's Encrypt's roots (ISRG X1, X2, Root YE, Root YR): auto (when the served chain is theirs), letsencrypt, none")
	flags.Func("ca", "extra CA certificate PEM file for the AP's bundle (repeatable)", func(value string) error {
		if value == "" {
			return errors.New("empty CA file")
		}
		options.extraCAs = append(options.extraCAs, value)
		return nil
	})
	flags.BoolVar(&options.pinIP, "pin-ip", false, "pin the AP to the address this command reaches it at (required in DNS mode)")
	flags.BoolVar(&options.noReboot, "no-reboot", false, "install and register, but do not reboot the AP")
	flags.BoolVar(&options.removeLocal, "remove-local-files", false, "move /rw/etc/mist/ep-control/local_* aside (kept in the backup and on the AP)")
	flags.BoolVar(&options.dryRun, "dry-run", false, "check everything and print the plan; write nothing")
	flags.BoolVar(&options.yes, "yes", false, "answer yes: keep local_* files, reboot an AP that is serving clients")
	flags.BoolVar(&options.retarget, "retarget", false, "only rewrite cloud.ini and the CA bundle (the AP is already registered)")
	flags.DurationVar(&options.timeout, "timeout", defaultOnboardWait, "how long to wait for the AP to come back in sync")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(onboardUsage)
	}
	if options.controller == "" || options.tokenFile == "" {
		return errors.New("--controller and --token-file are required\n" + onboardUsage)
	}
	if !options.retarget && (strings.TrimSpace(options.site) == "" || strings.TrimSpace(options.name) == "") {
		return errors.New("--name is required, and --site must not be empty (--retarget keeps the AP's registration and needs neither)\n" + onboardUsage)
	}
	if options.retarget && options.removeLocal {
		return errors.New("--retarget only rewrites cloud.ini and the CA bundle; run onboard without --retarget to deal with local files")
	}
	if !slices.Contains([]string{publicRootsAuto, publicRootsLE, publicRootsNone}, options.publicRoots) {
		return errors.New("--public-roots must be auto, letsencrypt or none")
	}
	if options.timeout <= 0 || options.timeout > time.Hour {
		return errors.New("--timeout must be between 1s and 1h")
	}
	if len(strings.TrimSpace(options.name)) > 64 {
		return errors.New("--name must be at most 64 characters")
	}
	return (&onboarding{env: env, options: options, ssh: ssh, destination: positional[0]}).run()
}

// onboarding is one run of the command.
type onboarding struct {
	env         ap33Env
	options     onboardOptions
	ssh         sshFlags
	destination string

	api      *controllerClient
	view     api.OnboardingView
	endpoint string // the URL both [[cloud]] entries get; "" keeps the stock name (DNS mode)
	site     api.SiteSummary
	extraCAs []*x509.Certificate
}

func (o *onboarding) step(format string, args ...any) {
	fmt.Fprintf(o.env.stdout, format+"\n", args...)
}

func (o *onboarding) run() error {
	ctx := o.env.ctx
	token, err := readTokenFile(o.options.tokenFile, o.env.stderr)
	if err != nil {
		return withExit(exitPreflight, err)
	}
	if o.api, err = newControllerClient(o.options.controller, token); err != nil {
		return withExit(exitPreflight, err)
	}
	if err := o.controllerPreflight(ctx); err != nil {
		return err
	}

	client, target, err := connectAP(o.env, &o.ssh, o.destination)
	if err != nil {
		return withExit(exitPreflight, err)
	}
	defer client.Close()
	report, err := collectInspect(ctx, client, inspectOptions{Level: inspectFull})
	if err != nil {
		return exitf(exitPreflight, "preflight %s: %w", target, err)
	}
	report.Target = target.String()
	report.HostKey = client.HostKeyFingerprint()
	eepromPath, err := onboardPreflight(report)
	if err != nil {
		return withExit(exitPreflight, err)
	}
	o.step("Preflight: AP %s, %s on %s, boot config %s, uptime %s", report.ID, firmwareLabel(report), orUnknown(report.RootFS),
		bootConfigLabel(report), uptimeLabel(report))

	existing, err := o.api.getAP(ctx, report.ID)
	if err != nil {
		return withExit(exitAPIError, err)
	}
	if o.options.retarget && existing == nil {
		return exitf(exitAPIError, "%s is not registered at %s; run onboard without --retarget to register it", report.ID, o.api)
	}
	o.describeRegistration(report.ID, existing)
	pinnedIP := ""
	if o.options.pinIP {
		pinnedIP = client.RemoteIP()
		if net.ParseIP(pinnedIP) == nil {
			return exitf(exitPreflight, "--pin-ip: cannot tell the AP's address from the SSH connection (%q)", pinnedIP)
		}
	}

	if err := o.checkLocalFiles(report); err != nil {
		return err
	}

	plan, err := o.planTrust(ctx, client, report)
	if err != nil {
		return err
	}
	if o.options.dryRun {
		o.printDryRun(report, existing, plan)
		return nil
	}

	root := o.options.backupDir
	if root == "" {
		if root, err = defaultBackupRoot(); err != nil {
			return withExit(exitPreflight, err)
		}
	}
	root = expandHome(root)
	backup, err := createBackup(ctx, client, report, root, eepromPath, client.HostKey(), o.env.now())
	if err != nil {
		return withExit(exitPreflight, err)
	}
	o.step("Backup: %s (%s %d bytes, %s %d bytes, %s)", backup.Dir, backupTarName, backup.TarSize, backupEEPROMName, len(backup.EEPROM), backupSumsName)
	var secret []byte
	if !o.options.retarget {
		if secret, err = apSecret(backup.EEPROM); err != nil {
			return withExit(exitPreflight, err)
		}
		o.step("Secret: read %d bytes at offset 0x%x of %s (kept in memory, not shown)", len(secret), secretOffset, eepromPath)
	}

	// From here on the AP changes. The bundle goes first: it only adds
	// certificates, so an interruption before cloud.ini leaves the AP on
	// its old endpoint with a superset of its old trust.
	changed := false
	localFiles := localFileViews(report.LocalFiles, o.env.now())
	if o.options.removeLocal && len(report.LocalFiles) > 0 {
		aside := fmt.Sprintf("%s/%s", localFilesAsideRoot, filepath.Base(backup.Dir))
		for _, file := range report.LocalFiles {
			if err := moveAPFile(ctx, client, apEPControlRW+"/"+file.Name, aside+"/"+file.Name); err != nil {
				return withExit(exitPreflight, fmt.Errorf("%w (restore with `dismist ap33 restore --backup %s`)", err, backup.Dir))
			}
			o.step("Local file %s: moved to %s/ (also in the backup)", file.Name, aside)
		}
		localFiles = []api.LocalFileView{}
		changed = true
	}
	if plan.writeBundle {
		if err := writeAPFile(ctx, client, rwRootCertsPath, plan.bundle); err != nil {
			return withExit(exitPreflight, fmt.Errorf("%w (restore with `dismist ap33 restore --backup %s`)", err, backup.Dir))
		}
		o.step("Installed %s (%d certificates, sha256 verified on the AP)", rwRootCertsPath, plan.bundleCerts)
		changed = true
	}
	if plan.writeINI {
		if err := writeAPFile(ctx, client, rwCloudINIPath, plan.cloudINI); err != nil {
			return withExit(exitPreflight, fmt.Errorf("%w (restore with `dismist ap33 restore --backup %s`)", err, backup.Dir))
		}
		o.step("Installed %s (sha256 verified on the AP)", rwCloudINIPath)
		changed = true
	}

	if !o.options.retarget {
		action, err := upsertAP(ctx, o.api, registration{
			ID: report.ID, Name: strings.TrimSpace(o.options.name), SiteID: o.site.ID, Secret: secret,
			PinnedIP: pinnedIP, LocalFiles: localFiles,
		}, o.env.stdout)
		if err != nil {
			hint := "the AP has not been rebooted and keeps its current connection"
			if changed {
				hint = "the new files are installed but the AP has not been rebooted, so it keeps its current connection until it restarts; fix the problem and re-run (the files are already in place), or undo them with `dismist ap33 restore --backup " + backup.Dir + "`"
			}
			return exitf(exitAPIError, "register %s: %w; %s", report.ID, err, hint)
		}
		o.step("Registration: %s %s in site %s (%s)", report.ID, action, o.site.Name, o.site.ID)
	}

	return o.rebootAndVerify(ctx, client, report, existing, changed)
}

// controllerPreflight reads everything onboard needs from the controller
// before it connects to the AP: the token's scope, the endpoint and CAs,
// and the site.
func (o *onboarding) controllerPreflight(ctx context.Context) error {
	session, err := o.api.session(ctx)
	if err != nil {
		return withExit(exitAPIError, err)
	}
	if !session.Authenticated {
		return exitf(exitAPIError, "%s did not accept the API token", o.api)
	}
	if !o.options.dryRun && !o.options.retarget && session.Scope != "" && session.Scope != api.TokenScopeAdmin {
		return exitf(exitAPIError, "the API token has %q scope; registering an AP needs an admin-scope token", session.Scope)
	}
	if o.view, err = o.api.onboarding(ctx); err != nil {
		return withExit(exitAPIError, err)
	}
	if o.view.DNSMode {
		if !o.options.retarget && !o.options.pinIP {
			return exitf(exitPreflight, "%s runs in DNS mode, which answers only pinned APs: re-run with --pin-ip", o.api)
		}
	} else {
		if o.view.WSSURL == "" {
			return exitf(exitAPIError, "%s does not report its AP endpoint (GET /api/onboarding wss_url is empty): set the controller's -ap-url", o.api)
		}
		if err := checkCloudURL(o.view.WSSURL); err != nil {
			return exitf(exitAPIError, "the controller's AP endpoint: %w", err)
		}
		o.endpoint = o.view.WSSURL
	}
	for _, ca := range o.view.CAPems {
		certs, err := parsePEMCertificates([]byte(ca.PEM))
		if err != nil {
			return exitf(exitAPIError, "the controller's CA %q: %w", ca.Name, err)
		}
		o.extraCAs = append(o.extraCAs, certs...)
	}
	for _, file := range o.options.extraCAs {
		data, err := os.ReadFile(expandHome(file))
		if err != nil {
			return withExit(exitPreflight, fmt.Errorf("--ca: %w", err))
		}
		certs, err := parsePEMCertificates(data)
		if err != nil {
			return exitf(exitPreflight, "--ca %s: %w", file, err)
		}
		o.extraCAs = append(o.extraCAs, certs...)
	}
	if !o.options.retarget {
		sites, err := o.api.sites(ctx)
		if err != nil {
			return withExit(exitAPIError, err)
		}
		if o.site, err = resolveSite(sites, o.options.site); err != nil {
			return withExit(exitPreflight, err)
		}
	}
	mode := "endpoint " + o.endpoint
	if o.view.DNSMode {
		mode = "DNS mode (the AP keeps the stock Mist name)"
	}
	o.step("Controller: %s, %s, %d CA certificate(s) to add", o.api, mode, len(o.extraCAs))
	return nil
}

// onboardPreflight checks the AP is one onboard knows how to change and
// returns the EEPROM path (design §3.15 step 2).
func onboardPreflight(report *InspectReport) (string, error) {
	var problems []string
	if report.UID == nil || *report.UID != 0 {
		problems = append(problems, "the SSH login is not root (id -u is not 0)")
	}
	if report.ID == "" || len(report.Interfaces) == 0 || report.Interfaces[0].Name != "eth0" {
		problems = append(problems, "cannot read the AP ID from /sys/class/net/eth0/address")
	}
	if report.Firmware.Version == "" {
		problems = append(problems, apVersionFile+" is missing or has no FIRMWARE_VERSION: this may not be a Mist AP")
	}
	if report.Firmware.FIPS != "" && report.Firmware.FIPS != "0" {
		problems = append(problems, "FIPS firmware is not supported by onboard (its bundle and endpoint names differ)")
	}
	var eeprom string
	sized := 0
	for _, info := range report.EEPROMs {
		if info.Size == eepromSize {
			sized++
			eeprom = info.Path
		}
	}
	if sized != 1 {
		problems = append(problems, fmt.Sprintf("expected exactly one %d-byte EEPROM under %s, found %d", eepromSize, apEEPROMGlob, sized))
	}
	if !strings.HasPrefix(eeprom, "/sys/bus/i2c/devices/") && sized == 1 {
		problems = append(problems, fmt.Sprintf("unexpected EEPROM path %q", eeprom))
	}
	if report.Cloud == nil {
		problems = append(problems, "cannot read the AP's cloud.ini")
	}
	if len(problems) > 0 {
		return "", fmt.Errorf("preflight failed, nothing was changed:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return eeprom, nil
}

func (o *onboarding) describeRegistration(id string, existing *api.APDetail) {
	switch {
	case existing == nil:
		o.step("Registration: %s is not registered at %s yet", id, o.api)
	case existing.Connected:
		o.step("Registration: %s is registered as %q and connected (rev %d applied of %d)", id, existing.Name, existing.AppliedRevision, existing.ConfigRevision)
	default:
		o.step("Registration: %s is registered as %q, not connected", id, existing.Name)
	}
}

// checkLocalFiles reports the AP's local_* files (design §3.15 step 3) and
// asks before continuing with them in place.
func (o *onboarding) checkLocalFiles(report *InspectReport) error {
	if len(report.LocalFiles) == 0 {
		o.step("Local files: none in %s", apEPControlRW)
		return nil
	}
	for _, file := range report.LocalFiles {
		o.step("Local file %s: %d bytes, sha256 %s", file.Name, file.Size, file.SHA256)
	}
	if o.options.removeLocal || o.options.dryRun || o.options.retarget {
		return nil
	}
	if o.options.yes {
		o.step("Local files: kept (--yes); they are reported to the controller")
		return nil
	}
	question := fmt.Sprintf("%d local_* file(s) change what this AP runs, so it will not run exactly the config dismist shows. Keep them and continue?", len(report.LocalFiles))
	ok, err := o.env.ask(question)
	if err != nil {
		return withExit(exitLocalFilesSet, err)
	}
	if !ok {
		return exitf(exitLocalFilesSet, "%d local_* file(s) present in %s and not confirmed: re-run with --yes to keep them, or --remove-local-files to move them aside (nothing was changed)", len(report.LocalFiles), apEPControlRW)
	}
	return nil
}

// trustPlan is what onboard will write for the endpoint and trust.
type trustPlan struct {
	cloudINI    []byte
	bundle      []byte
	bundleCerts int
	added       []*x509.Certificate
	writeINI    bool
	writeBundle bool
	anchor      string
}

// planTrust builds cloud.ini and the CA bundle from the AP's own files
// (design §3.15 steps 5 and 6) and checks the chain the endpoint serves
// against the new bundle before anything is written.
func (o *onboarding) planTrust(ctx context.Context, client apStreamer, report *InspectReport) (*trustPlan, error) {
	stockINI, ok, err := readAPFile(ctx, client, stockCloudINIPath)
	if err != nil {
		return nil, withExit(exitPreflight, err)
	}
	if !ok {
		return nil, exitf(exitPreflight, "cannot read the firmware's %s", stockCloudINIPath)
	}
	cloudINI, err := rewriteCloudINI(stockINI, o.endpoint)
	if err != nil {
		return nil, exitf(exitPreflight, "%s: %w", stockCloudINIPath, err)
	}
	basePath := bundleInUse(report)
	base, ok, err := readAPFile(ctx, client, basePath)
	if err != nil {
		return nil, withExit(exitPreflight, err)
	}
	if !ok {
		return nil, exitf(exitPreflight, "cannot read the CA bundle the AP uses now (%s)", basePath)
	}

	var chain servedChain
	if o.endpoint != "" {
		if chain, err = fetchServedChain(ctx, o.endpoint, servedChainTimeout); err != nil {
			return nil, withExit(exitTLSMismatch, err)
		}
	}
	extras := slices.Clone(o.extraCAs)
	if o.options.publicRoots != publicRootsNone {
		roots, err := letsEncryptRoots()
		if err != nil {
			return nil, withExit(exitPreflight, err)
		}
		if o.options.publicRoots == publicRootsLE || chain.chainsToAny(roots, o.env.now()) {
			extras = append(extras, roots...)
		}
	}
	bundle, added, err := mergeCABundle(base, extras)
	if err != nil {
		return nil, withExit(exitPreflight, err)
	}
	certs, err := parsePEMCertificates(bundle)
	if err != nil {
		return nil, withExit(exitPreflight, err)
	}
	plan := &trustPlan{cloudINI: []byte(cloudINI), bundle: bundle, bundleCerts: len(certs), added: added}
	if o.endpoint != "" {
		anchor, err := chain.verify(certPool(certs), o.env.now())
		if err != nil {
			printServedChain(o.env.stderr, chain)
			return nil, exitf(exitTLSMismatch, "%s: %w; nothing was changed. The bundle needs the root that issues the endpoint's certificate: check the controller's CA list (GET /api/onboarding) or pass --ca", o.endpoint, err)
		}
		plan.anchor = anchor.Subject.String()
		o.step("TLS: the chain %s serves verifies against the new bundle, anchored at %s", chain.Addr, plan.anchor)
	} else {
		o.step("TLS: DNS mode; the served chain is not checked from here (the bundle includes every CA the controller lists)")
	}

	currentBundle, haveBundle, err := readAPFile(ctx, client, rwRootCertsPath)
	if err != nil {
		return nil, withExit(exitPreflight, err)
	}
	plan.writeBundle = !haveBundle || !bytes.Equal(currentBundle, bundle)
	currentINI, haveINI := []byte(nil), false
	if slices.Contains(report.RWFiles, "cloud.ini") {
		if currentINI, haveINI, err = readAPFile(ctx, client, rwCloudINIPath); err != nil {
			return nil, withExit(exitPreflight, err)
		}
	}
	plan.writeINI = !haveINI || !cloudINIEquivalent(currentINI, plan.cloudINI)

	switch {
	case !plan.writeBundle:
		o.step("CA bundle: %s already holds every CA (%d certificates); unchanged", rwRootCertsPath, plan.bundleCerts)
	case len(added) == 0:
		o.step("CA bundle: copy %s to %s (%d certificates)", basePath, rwRootCertsPath, plan.bundleCerts)
	default:
		names := make([]string, len(added))
		for i, cert := range added {
			names[i] = describeCert(cert)
		}
		o.step("CA bundle: %s plus %d certificate(s): %s", basePath, len(added), strings.Join(names, "; "))
	}
	target := o.endpoint
	if target == "" {
		target = "the stock Mist name"
	}
	if plan.writeINI {
		o.step("cloud.ini: point both [[cloud]] entries at %s, rootcerts at %s", target, rwRootCertsPath)
	} else {
		o.step("cloud.ini: %s already points at %s; unchanged", rwCloudINIPath, target)
	}
	return plan, nil
}

// bundleInUse is the CA bundle the AP's current cloud.ini names for its
// production (non-factory) entry.
func bundleInUse(report *InspectReport) string {
	if report.Cloud != nil {
		for _, entry := range report.Cloud.Entries {
			if !entry.Manufacturing && entry.RootCerts != "" {
				return strings.ReplaceAll(entry.RootCerts, "${FIPS}", "")
			}
		}
		for _, entry := range report.Cloud.Entries {
			if entry.RootCerts != "" {
				return strings.ReplaceAll(entry.RootCerts, "${FIPS}", "")
			}
		}
		if report.Cloud.TLSRootCerts != "" {
			return strings.ReplaceAll(report.Cloud.TLSRootCerts, "${FIPS}", "")
		}
	}
	return stockBundlePath
}

func (o *onboarding) printDryRun(report *InspectReport, existing *api.APDetail, plan *trustPlan) {
	o.step("Dry run: nothing was written. With the same inputs onboard would:")
	o.step("  - back up /rw and the EEPROM to a new directory under %s", orDefault(o.options.backupDir, "~/.local/share/dismist/backups"))
	if o.options.removeLocal && len(report.LocalFiles) > 0 {
		o.step("  - move %d local_* file(s) to %s/<backup time>/", len(report.LocalFiles), localFilesAsideRoot)
	}
	if plan.writeBundle {
		o.step("  - write %s (%d certificates)", rwRootCertsPath, plan.bundleCerts)
	}
	if plan.writeINI {
		o.step("  - write %s", rwCloudINIPath)
	}
	if !o.options.retarget {
		if existing == nil {
			o.step("  - register %s as %q in site %s (POST /api/aps)", report.ID, strings.TrimSpace(o.options.name), o.site.Name)
		} else {
			o.step("  - refresh the registration of %s (PATCH /api/aps/%s, If-Match %d)", report.ID, report.ID, existing.UpdatedRev)
		}
	}
	switch {
	case !plan.writeBundle && !plan.writeINI && !(o.options.removeLocal && len(report.LocalFiles) > 0):
		o.step("  - not reboot (nothing on the AP changes)")
	case o.options.noReboot:
		o.step("  - not reboot (--no-reboot)")
	default:
		o.step("  - reboot the AP and wait up to %s for it to come back in sync", o.options.timeout)
	}
}

// rebootAndVerify reboots the AP when its files changed (or when they were
// already right but the AP is not connected here, as after an earlier
// --no-reboot run) and waits until it is connected and in sync (design
// §3.15 step 9). An AP this controller sees connected is serving clients,
// so rebooting it needs --yes or a yes on the terminal.
func (o *onboarding) rebootAndVerify(ctx context.Context, client *apSSH, report *InspectReport, existing *api.APDetail, changed bool) error {
	before, err := o.api.getAP(ctx, report.ID)
	if err != nil {
		return withExit(exitAPIError, err)
	}
	if before == nil {
		before = existing
	}
	connected := before != nil && before.Connected
	if !changed {
		if connected {
			o.step("Reboot: not needed, nothing on the AP changed")
			return o.wait(ctx, report.ID, nil, false)
		}
		o.step("Reboot: nothing on the AP changed, but it is not connected to %s; it may not have restarted since the files were installed", o.api)
	}
	if o.options.noReboot {
		o.step("Reboot: skipped (--no-reboot). The AP uses the new files from its next restart: `dismist ap33 ssh %s reboot --yes`", o.destination)
		return nil
	}
	if connected && !o.options.yes {
		ok, err := o.env.ask(onboardConfirmReboot)
		if err != nil || !ok {
			o.step("Reboot: skipped (not confirmed). The files and registration are in place; reboot when it suits with `dismist ap33 ssh %s reboot --yes`, or re-run with --yes", o.destination)
			return nil
		}
	}
	if err := requestReboot(ctx, client, onboardRebootReason); err != nil {
		return exitf(exitPreflight, "reboot %s: %w", report.ID, err)
	}
	o.step("Reboot: requested (reason %q); waiting up to %s for %s to reconnect", onboardRebootReason, o.options.timeout, report.ID)
	var previous *time.Time
	if before != nil {
		previous = before.ConnectedAt
	}
	return o.wait(ctx, report.ID, previous, true)
}

// wait polls GET /api/aps/<id> until the AP is connected with its applied
// revision equal to its config revision. With requireNew it also needs a
// session newer than previous, so the pre-reboot session does not count.
func (o *onboarding) wait(ctx context.Context, id string, previous *time.Time, requireNew bool) error {
	interval := o.env.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	deadline := time.NewTimer(o.options.timeout)
	defer deadline.Stop()
	lastState := ""
	var last *api.APDetail
	for {
		detail, err := o.api.getAP(ctx, id)
		if err != nil {
			return withExit(exitAPIError, err)
		}
		if detail == nil {
			return exitf(exitAPIError, "%s disappeared from the controller while waiting", id)
		}
		last = detail
		fresh := !requireNew || (detail.ConnectedAt != nil && (previous == nil || !detail.ConnectedAt.Equal(*previous)))
		if detail.Connected && fresh && detail.ConfigRevision > 0 && detail.AppliedRevision == detail.ConfigRevision {
			o.step("Done: %s (%s, %s) is connected and in sync at rev %d; LAN address %s, public address %s",
				id, orUnknown(detail.Model), orUnknown(detail.Firmware), detail.ConfigRevision, orUnknown(detail.LANIP), orUnknown(detail.RemoteAddress))
			return nil
		}
		state := string(detail.Status)
		if detail.Connected && !fresh {
			state = "rebooting"
		}
		if detail.LastError != "" {
			state += ": " + detail.LastError
		}
		if state != lastState {
			o.step("Waiting: %s", state)
			lastState = state
		}
		select {
		case <-ctx.Done():
			return withExit(exitTimedOut, ctx.Err())
		case <-deadline.C:
			return exitf(exitTimedOut, "%s did not come back in sync within %s (last status %s, rev %d applied of %d%s). Check `dismist ap33 inspect %s` (cloud URL, CA bundle, ep-control log) and the controller's pending APs",
				id, o.options.timeout, last.Status, last.AppliedRevision, last.ConfigRevision, errorSuffix(last.LastError), o.destination)
		case <-time.After(interval):
		}
	}
}

func errorSuffix(message string) string {
	if message == "" {
		return ""
	}
	return ", last error " + message
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// localFileViews is the local_files list for the API: names and SHA-256
// only, never the contents.
func localFileViews(files []LocalFile, now time.Time) []api.LocalFileView {
	views := make([]api.LocalFileView, 0, len(files))
	for _, file := range files {
		if file.SHA256 == "" {
			continue
		}
		views = append(views, api.LocalFileView{Name: file.Name, SHA256: file.SHA256, SeenAt: now.UTC()})
	}
	return views
}

func firmwareLabel(report *InspectReport) string {
	label := report.Firmware.Version
	if report.Firmware.Model != "" {
		label += " (" + report.Firmware.Model + ")"
	}
	return label
}

func bootConfigLabel(report *InspectReport) string {
	if report.BootConfig == nil {
		return "unknown"
	}
	return report.BootConfig.Raw
}

func uptimeLabel(report *InspectReport) string {
	if report.UptimeSeconds == nil {
		return "unknown"
	}
	return formatUptime(time.Duration(*report.UptimeSeconds * float64(time.Second)))
}
