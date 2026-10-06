package main

import (
	"context"
	"sync"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/setup"
)

// wizardOps is every side effect the interactive init wizard performs.
// Stages call these only from inside tea.Cmds (never from Update), so
// the UI keeps redrawing while they run; tests substitute a stub.
type wizardOps interface {
	Probe(ctx context.Context, canonical string) setup.Reach
	CheckPin(ctx context.Context, cfg *config.Config, canonical string, insecure bool, accepted string) (string, *pinChange)
	// CreateToken logs in and creates the token; with replace it first
	// deletes an existing token of the same name.
	CreateToken(ctx context.Context, baseURL string, insecure bool, pin, user, password, name string, replace bool) (tokenID, secret string, err error)
	VerifyToken(ctx context.Context, baseURL, tokenID, secret string, knownInsecure bool, pin string) (bool, error)
	ListNodes(ctx context.Context, client *pveclient.Client) ([]pveclient.Node, error)
	NodeResources(ctx context.Context, client *pveclient.Client, node string) nodeResources
	EnableSnippets(ctx context.Context, client *pveclient.Client, storage string, content []string) error
	HostKnown(host string) (known bool, knownHosts string, err error)
	FetchHostKey(ctx context.Context, host string) (pvessh.HostKey, error)
	PinHostKey(knownHosts string, k pvessh.HostKey) error
	ValidateSSH(ctx context.Context, cfg pvessh.Config) error
	Persist(cfg *config.Config, in persistInput) (*config.Server, []notice, error)
	CloudInit(canonical, user, sshKey string) cloudInitResult
}

// nodeResources is everything the Defaults page lists for one node,
// fetched concurrently. Each list keeps its own error so one missing
// permission only turns that one field into manual entry.
type nodeResources struct {
	templates     []pveclient.Template
	templateTotal int
	templateErr   error
	storage       []pveclient.Storage
	storageErr    error
	bridges       []pveclient.Bridge
	bridgeErr     error
}

// newWizardOps is a seam so tests can run the wizard against a stub.
var newWizardOps = func() wizardOps { return prodWizardOps{} }

type prodWizardOps struct{}

func (prodWizardOps) Probe(ctx context.Context, canonical string) setup.Reach {
	return setup.ProbeTLS(ctx, probeEndpoint, canonical)
}

func (prodWizardOps) CheckPin(ctx context.Context, cfg *config.Config, canonical string, insecure bool, accepted string) (string, *pinChange) {
	return checkPin(ctx, cfg, canonical, insecure, accepted)
}

func (prodWizardOps) CreateToken(ctx context.Context, baseURL string, insecure bool, pin, user, password, name string, replace bool) (string, string, error) {
	issuer, err := setup.Login(ctx, baseURL, insecure, pin, user, password)
	if err != nil {
		return "", "", err
	}
	if replace {
		return issuer.Replace(ctx, name)
	}
	return issuer.Create(ctx, name)
}

func (prodWizardOps) VerifyToken(ctx context.Context, baseURL, tokenID, secret string, knownInsecure bool, pin string) (bool, error) {
	return setup.VerifyToken(ctx, baseURL, tokenID, secret, knownInsecure, pin)
}

func (prodWizardOps) ListNodes(ctx context.Context, client *pveclient.Client) ([]pveclient.Node, error) {
	dctx, cancel := discoveryCtx(ctx)
	defer cancel()
	return client.ListNodes(dctx)
}

func (prodWizardOps) NodeResources(ctx context.Context, client *pveclient.Client, node string) nodeResources {
	var (
		r  nodeResources
		wg sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		dctx, cancel := discoveryCtx(ctx)
		defer cancel()
		r.templates, r.templateTotal, r.templateErr = client.ListTemplates(dctx, node)
	}()
	go func() {
		defer wg.Done()
		dctx, cancel := discoveryCtx(ctx)
		defer cancel()
		r.storage, r.storageErr = client.ListStorage(dctx, node)
	}()
	go func() {
		defer wg.Done()
		dctx, cancel := discoveryCtx(ctx)
		defer cancel()
		r.bridges, r.bridgeErr = client.ListBridges(dctx, node)
	}()
	wg.Wait()
	return r
}

func (prodWizardOps) EnableSnippets(ctx context.Context, client *pveclient.Client, storage string, content []string) error {
	return client.UpdateStorageContent(ctx, storage, content)
}

func (prodWizardOps) HostKnown(host string) (bool, string, error) {
	kh, err := sshKnownHostsPathFn()
	if err != nil {
		return false, "", err
	}
	known, err := pvessh.KnownHostsHas(kh, host)
	return known, kh, err
}

func (prodWizardOps) FetchHostKey(ctx context.Context, host string) (pvessh.HostKey, error) {
	return pvessh.FetchHostKey(ctx, host)
}

func (prodWizardOps) PinHostKey(knownHosts string, k pvessh.HostKey) error {
	return pvessh.AppendKnownHost(knownHosts, k)
}

func (prodWizardOps) ValidateSSH(ctx context.Context, cfg pvessh.Config) error {
	return sshValidateFn(ctx, cfg)
}

func (prodWizardOps) Persist(cfg *config.Config, in persistInput) (*config.Server, []notice, error) {
	return persistCore(cfg, in)
}

func (prodWizardOps) CloudInit(canonical, user, sshKey string) cloudInitResult {
	return ensureCloudInit(canonical, user, sshKey)
}
