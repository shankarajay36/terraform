package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/cli"
	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/terraform/addrs"
	"github.com/hashicorp/terraform/backend/local"
	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/helper/copy"
	"github.com/hashicorp/terraform/internal/getproviders"
	"github.com/hashicorp/terraform/internal/providercache"
	"github.com/hashicorp/terraform/state"
	"github.com/hashicorp/terraform/states"
	"github.com/hashicorp/terraform/states/statemgr"
	"github.com/hashicorp/terraform/terraform"
)

func TestInit_empty(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	os.MkdirAll(td, 0755)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}
}

func TestInit_multipleArgs(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	os.MkdirAll(td, 0755)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{
		"bad",
		"bad",
	}
	if code := c.Run(args); code != 1 {
		t.Fatalf("bad: \n%s", ui.OutputWriter.String())
	}
}

func TestInit_fromModule_explicitDest(t *testing.T) {
	td := tempDir(t)
	os.MkdirAll(td, 0755)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	if _, err := os.Stat(DefaultStateFilename); err == nil {
		// This should never happen; it indicates a bug in another test
		// is causing a terraform.tfstate to get left behind in our directory
		// here, which can interfere with our init process in a way that
		// isn't relevant to this test.
		fullPath, _ := filepath.Abs(DefaultStateFilename)
		t.Fatalf("some other test has left terraform.tfstate behind:\n%s", fullPath)
	}

	args := []string{
		"-from-module=" + testFixturePath("init"),
		td,
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	if _, err := os.Stat(filepath.Join(td, "hello.tf")); err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestInit_fromModule_cwdDest(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	os.MkdirAll(td, os.ModePerm)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{
		"-from-module=" + testFixturePath("init"),
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	if _, err := os.Stat(filepath.Join(td, "hello.tf")); err != nil {
		t.Fatalf("err: %s", err)
	}
}

// https://github.com/hashicorp/terraform/issues/518
func TestInit_fromModule_dstInSrc(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("err: %s", err)
	}
	defer os.RemoveAll(dir)

	// Change to the temporary directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("err: %s", err)
	}
	defer os.Chdir(cwd)

	if err := os.Mkdir("foo", os.ModePerm); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Create("issue518.tf"); err != nil {
		t.Fatalf("err: %s", err)
	}

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{
		"-from-module=.",
		"foo",
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "foo", "issue518.tf")); err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestInit_get(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// Check output
	output := ui.OutputWriter.String()
	if !strings.Contains(output, "foo in foo") {
		t.Fatalf("doesn't look like we installed module 'foo': %s", output)
	}
}

func TestInit_getUpgradeModules(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	os.MkdirAll(td, 0755)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{
		"-get=true",
		"-get-plugins=false",
		"-upgrade",
		testFixturePath("init-get"),
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("command did not complete successfully:\n%s", ui.ErrorWriter.String())
	}

	// Check output
	output := ui.OutputWriter.String()
	if !strings.Contains(output, "Upgrading modules...") {
		t.Fatalf("doesn't look like get upgrade: %s", output)
	}
}

func TestInit_backend(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	if _, err := os.Stat(filepath.Join(DefaultDataDir, DefaultStateFilename)); err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestInit_backendUnset(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	{
		log.Printf("[TRACE] TestInit_backendUnset: beginning first init")

		ui := cli.NewMockUi()
		c := &InitCommand{
			Meta: Meta{
				testingOverrides: metaOverridesForProvider(testProvider()),
				Ui:               ui,
			},
		}

		// Init
		args := []string{}
		if code := c.Run(args); code != 0 {
			t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
		}
		log.Printf("[TRACE] TestInit_backendUnset: first init complete")
		t.Logf("First run output:\n%s", ui.OutputWriter.String())
		t.Logf("First run errors:\n%s", ui.ErrorWriter.String())

		if _, err := os.Stat(filepath.Join(DefaultDataDir, DefaultStateFilename)); err != nil {
			t.Fatalf("err: %s", err)
		}
	}

	{
		log.Printf("[TRACE] TestInit_backendUnset: beginning second init")

		// Unset
		if err := ioutil.WriteFile("main.tf", []byte(""), 0644); err != nil {
			t.Fatalf("err: %s", err)
		}

		ui := cli.NewMockUi()
		c := &InitCommand{
			Meta: Meta{
				testingOverrides: metaOverridesForProvider(testProvider()),
				Ui:               ui,
			},
		}

		args := []string{"-force-copy"}
		if code := c.Run(args); code != 0 {
			t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
		}
		log.Printf("[TRACE] TestInit_backendUnset: second init complete")
		t.Logf("Second run output:\n%s", ui.OutputWriter.String())
		t.Logf("Second run errors:\n%s", ui.ErrorWriter.String())

		s := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
		if !s.Backend.Empty() {
			t.Fatal("should not have backend config")
		}
	}
}

func TestInit_backendConfigFile(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend-config-file"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	t.Run("good-config-file", func(t *testing.T) {
		ui := new(cli.MockUi)
		c := &InitCommand{
			Meta: Meta{
				testingOverrides: metaOverridesForProvider(testProvider()),
				Ui:               ui,
			},
		}
		args := []string{"-backend-config", "input.config"}
		if code := c.Run(args); code != 0 {
			t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
		}

		// Read our saved backend config and verify we have our settings
		state := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
		if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"hello","workspace_dir":null}`; got != want {
			t.Errorf("wrong config\ngot:  %s\nwant: %s", got, want)
		}
	})

	// the backend config file must be a set of key-value pairs and not a full backend {} block
	t.Run("invalid-config-file", func(t *testing.T) {
		ui := new(cli.MockUi)
		c := &InitCommand{
			Meta: Meta{
				testingOverrides: metaOverridesForProvider(testProvider()),
				Ui:               ui,
			},
		}
		args := []string{"-backend-config", "backend.config"}
		if code := c.Run(args); code != 1 {
			t.Fatalf("expected error, got success\n")
		}
		if !strings.Contains(ui.ErrorWriter.String(), "Invalid backend configuration file") {
			t.Fatalf("wrong error: %s", ui.ErrorWriter)
		}
	})
}

func TestInit_backendConfigFilePowershellConfusion(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend-config-file"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	// SUBTLE: when using -flag=value with Powershell, unquoted values are
	// broken into separate arguments. This results in the init command
	// interpreting the flags as an empty backend-config setting (which is
	// semantically valid!) followed by a custom configuration path.
	//
	// Adding the "=" here forces this codepath to be checked, and it should
	// result in an early exit with a diagnostic that the provided
	// configuration file is not a diretory.
	args := []string{"-backend-config=", "./input.config"}
	if code := c.Run(args); code != 1 {
		t.Fatalf("got exit status %d; want 1\nstderr:\n%s\n\nstdout:\n%s", code, ui.ErrorWriter.String(), ui.OutputWriter.String())
	}

	output := ui.ErrorWriter.String()
	if got, want := output, `Module directory ./input.config does not exist`; !strings.Contains(got, want) {
		t.Fatalf("wrong output\ngot:\n%s\n\nwant: message containing %q", got, want)
	}
}

func TestInit_backendConfigFileChange(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend-config-file-change"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// Ask input
	defer testInputMap(t, map[string]string{
		"backend-migrate-to-new": "no",
	})()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-backend-config", "input.config"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// Read our saved backend config and verify we have our settings
	state := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"hello","workspace_dir":null}`; got != want {
		t.Errorf("wrong config\ngot:  %s\nwant: %s", got, want)
	}
}

func TestInit_backendConfigKV(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend-config-kv"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-backend-config", "path=hello"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// Read our saved backend config and verify we have our settings
	state := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"hello","workspace_dir":null}`; got != want {
		t.Errorf("wrong config\ngot:  %s\nwant: %s", got, want)
	}
}

func TestInit_backendConfigKVReInit(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend-config-kv"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-backend-config", "path=test"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	ui = new(cli.MockUi)
	c = &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	// a second init should require no changes, nor should it change the backend.
	args = []string{"-input=false"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// make sure the backend is configured how we expect
	configState := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	cfg := map[string]interface{}{}
	if err := json.Unmarshal(configState.Backend.ConfigRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["path"] != "test" {
		t.Fatalf(`expected backend path="test", got path="%v"`, cfg["path"])
	}

	// override the -backend-config options by settings
	args = []string{"-input=false", "-backend-config", ""}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// make sure the backend is configured how we expect
	configState = testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	cfg = map[string]interface{}{}
	if err := json.Unmarshal(configState.Backend.ConfigRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["path"] != nil {
		t.Fatalf(`expected backend path="<nil>", got path="%v"`, cfg["path"])
	}
}

func TestInit_backendConfigKVReInitWithConfigDiff(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-input=false"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	ui = new(cli.MockUi)
	c = &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	// a second init with identical config should require no changes, nor
	// should it change the backend.
	args = []string{"-input=false", "-backend-config", "path=foo"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// make sure the backend is configured how we expect
	configState := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	cfg := map[string]interface{}{}
	if err := json.Unmarshal(configState.Backend.ConfigRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["path"] != "foo" {
		t.Fatalf(`expected backend path="foo", got path="%v"`, cfg["foo"])
	}
}

func TestInit_backendCli_no_config_block(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-backend-config", "path=test"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("got exit status %d; want 0\nstderr:\n%s\n\nstdout:\n%s", code, ui.ErrorWriter.String(), ui.OutputWriter.String())
	}

	errMsg := ui.ErrorWriter.String()
	if !strings.Contains(errMsg, "Warning: Missing backend configuration") {
		t.Fatal("expected missing backend block warning, got", errMsg)
	}
}

func TestInit_targetSubdir(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	os.MkdirAll(td, 0755)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// copy the source into a subdir
	copy.CopyDir(testFixturePath("init-backend"), filepath.Join(td, "source"))

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{
		"source",
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	if _, err := os.Stat(filepath.Join(td, DefaultDataDir, DefaultStateFilename)); err != nil {
		t.Fatalf("err: %s", err)
	}

	// a data directory should not have been added to out working dir
	if _, err := os.Stat(filepath.Join(td, "source", DefaultDataDir)); !os.IsNotExist(err) {
		t.Fatalf("err: %s", err)
	}
}

func TestInit_backendReinitWithExtra(t *testing.T) {
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend-empty"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	m := testMetaBackend(t, nil)
	opts := &BackendOpts{
		ConfigOverride: configs.SynthBody("synth", map[string]cty.Value{
			"path": cty.StringVal("hello"),
		}),
		Init: true,
	}

	_, cHash, err := m.backendConfig(opts)
	if err != nil {
		t.Fatal(err)
	}

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-backend-config", "path=hello"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// Read our saved backend config and verify we have our settings
	state := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"hello","workspace_dir":null}`; got != want {
		t.Errorf("wrong config\ngot:  %s\nwant: %s", got, want)
	}

	if state.Backend.Hash != uint64(cHash) {
		t.Fatal("mismatched state and config backend hashes")
	}

	// init again and make sure nothing changes
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}
	state = testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"hello","workspace_dir":null}`; got != want {
		t.Errorf("wrong config\ngot:  %s\nwant: %s", got, want)
	}
	if state.Backend.Hash != uint64(cHash) {
		t.Fatal("mismatched state and config backend hashes")
	}
}

// move option from config to -backend-config args
func TestInit_backendReinitConfigToExtra(t *testing.T) {
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	if code := c.Run([]string{"-input=false"}); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// Read our saved backend config and verify we have our settings
	state := testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"foo","workspace_dir":null}`; got != want {
		t.Errorf("wrong config\ngot:  %s\nwant: %s", got, want)
	}

	backendHash := state.Backend.Hash

	// init again but remove the path option from the config
	cfg := "terraform {\n  backend \"local\" {}\n}\n"
	if err := ioutil.WriteFile("main.tf", []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	// We need a fresh InitCommand here because the old one now has our configuration
	// file cached inside it, so it won't re-read the modification we just made.
	c = &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-input=false", "-backend-config=path=foo"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}
	state = testDataStateRead(t, filepath.Join(DefaultDataDir, DefaultStateFilename))
	if got, want := normalizeJSON(t, state.Backend.ConfigRaw), `{"path":"foo","workspace_dir":null}`; got != want {
		t.Errorf("wrong config after moving to arg\ngot:  %s\nwant: %s", got, want)
	}

	if state.Backend.Hash == backendHash {
		t.Fatal("state.Backend.Hash was not updated")
	}
}

// make sure inputFalse stops execution on migrate
func TestInit_inputFalse(t *testing.T) {
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-backend"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{"-input=false", "-backend-config=path=foo"}
	if code := c.Run([]string{"-input=false"}); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter)
	}

	// write different states for foo and bar
	fooState := states.BuildState(func(s *states.SyncState) {
		s.SetOutputValue(
			addrs.OutputValue{Name: "foo"}.Absolute(addrs.RootModuleInstance),
			cty.StringVal("foo"),
			false, // not sensitive
		)
	})
	if err := statemgr.NewFilesystem("foo").WriteState(fooState); err != nil {
		t.Fatal(err)
	}
	barState := states.BuildState(func(s *states.SyncState) {
		s.SetOutputValue(
			addrs.OutputValue{Name: "bar"}.Absolute(addrs.RootModuleInstance),
			cty.StringVal("bar"),
			false, // not sensitive
		)
	})
	if err := statemgr.NewFilesystem("bar").WriteState(barState); err != nil {
		t.Fatal(err)
	}

	ui = new(cli.MockUi)
	c = &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args = []string{"-input=false", "-backend-config=path=bar"}
	if code := c.Run(args); code == 0 {
		t.Fatal("init should have failed", ui.OutputWriter)
	}

	errMsg := ui.ErrorWriter.String()
	if !strings.Contains(errMsg, "input disabled") {
		t.Fatal("expected input disabled error, got", errMsg)
	}

	ui = new(cli.MockUi)
	c = &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	// A missing input=false should abort rather than loop infinitely
	args = []string{"-backend-config=path=baz"}
	if code := c.Run(args); code == 0 {
		t.Fatal("init should have failed", ui.OutputWriter)
	}
}

func TestInit_getProvider(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-providers"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	overrides := metaOverridesForProvider(testProvider())
	ui := new(cli.MockUi)
	providerSource, close := newMockProviderSource(t, map[string][]string{
		// looking for an exact version
		"exact": []string{"1.2.3"},
		// config requires >= 2.3.3
		"greater-than": []string{"2.3.4", "2.3.3", "2.3.0"},
		// config specifies
		"between": []string{"3.4.5", "2.3.4", "1.2.3"},
	})
	defer close()
	m := Meta{
		testingOverrides: overrides,
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{
		"-backend=false", // should be possible to install plugins without backend init
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// check that we got the providers for our config
	exactPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/hashicorp/exact/1.2.3/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(exactPath); os.IsNotExist(err) {
		t.Fatal("provider 'exact' not downloaded")
	}
	greaterThanPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/hashicorp/greater-than/2.3.4/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(greaterThanPath); os.IsNotExist(err) {
		t.Fatal("provider 'greater-than' not downloaded")
	}
	betweenPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/hashicorp/between/2.3.4/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(betweenPath); os.IsNotExist(err) {
		t.Fatal("provider 'between' not downloaded")
	}

	t.Run("future-state", func(t *testing.T) {
		// getting providers should fail if a state from a newer version of
		// terraform exists, since InitCommand.getProviders needs to inspect that
		// state.
		s := terraform.NewState()
		s.TFVersion = "100.1.0"
		local := &state.LocalState{
			Path: local.DefaultStateFilename,
		}
		if err := local.WriteState(s); err != nil {
			t.Fatal(err)
		}

		ui := new(cli.MockUi)
		m.Ui = ui
		c := &InitCommand{
			Meta: m,
		}

		if code := c.Run(nil); code == 0 {
			t.Fatal("expected error, got:", ui.OutputWriter)
		}

		errMsg := ui.ErrorWriter.String()
		if !strings.Contains(errMsg, "which is newer than current") {
			t.Fatal("unexpected error:", errMsg)
		}
	})
}

func TestInit_getProviderSource(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-provider-source"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	overrides := metaOverridesForProvider(testProvider())
	ui := new(cli.MockUi)
	providerSource, close := newMockProviderSource(t, map[string][]string{
		// looking for an exact version
		"acme/alpha": []string{"1.2.3"},
		// config doesn't specify versions for other providers
		"registry.example.com/acme/beta": []string{"1.0.0"},
		"gamma":                          []string{"2.0.0"},
	})
	defer close()
	m := Meta{
		testingOverrides: overrides,
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{
		"-backend=false", // should be possible to install plugins without backend init
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// check that we got the providers for our config
	exactPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/acme/alpha/1.2.3/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(exactPath); os.IsNotExist(err) {
		t.Fatal("provider 'alpha' not downloaded")
	}
	greaterThanPath := fmt.Sprintf(".terraform/plugins/registry.example.com/acme/beta/1.0.0/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(greaterThanPath); os.IsNotExist(err) {
		t.Fatal("provider 'beta' not downloaded")
	}
	betweenPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/hashicorp/gamma/2.0.0/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(betweenPath); os.IsNotExist(err) {
		t.Fatal("provider 'gamma' not downloaded")
	}
}

func TestInit_getProviderDetectedLegacy(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-provider-detected-legacy"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// We need to construct a multisource with a mock source and a registry
	// source: the mock source will return ErrRegistryProviderNotKnown for an
	// unknown provider, and the registry source will allow us to look up the
	// appropriate namespace if possible.
	providerSource, psClose := newMockProviderSource(t, map[string][]string{
		"hashicorp/foo":           []string{"1.2.3"},
		"terraform-providers/baz": []string{"2.3.4"}, // this will not be installed
	})
	defer psClose()
	registrySource, rsClose := testRegistrySource(t)
	defer rsClose()
	multiSource := getproviders.MultiSource{
		{Source: providerSource},
		{Source: registrySource},
	}

	ui := new(cli.MockUi)
	m := Meta{
		Ui:             ui,
		ProviderSource: multiSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{
		"-backend=false", // should be possible to install plugins without backend init
	}
	if code := c.Run(args); code == 0 {
		t.Fatalf("expected error, got output: \n%s", ui.OutputWriter.String())
	}

	// foo should be installed
	fooPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/hashicorp/foo/1.2.3/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(fooPath); os.IsNotExist(err) {
		t.Error("provider 'foo' not installed")
	}
	// baz should not be installed
	bazPath := fmt.Sprintf(".terraform/plugins/registry.terraform.io/terraform-providers/baz/2.3.4/%s", getproviders.CurrentPlatform)
	if _, err := os.Stat(bazPath); !os.IsNotExist(err) {
		t.Error("provider 'baz' installed, but should not be")
	}

	// error output is the main focus of this test
	errOutput := ui.ErrorWriter.String()
	errors := []string{
		"Error while installing hashicorp/frob:",
		"Could not find required providers, but found possible alternatives",
		"hashicorp/baz -> terraform-providers/baz",
		"terraform 0.13upgrade .",
		"terraform 0.13upgrade child",
		"The following remote modules must also be upgraded",
		"- module.dicerolls at acme/bar/random",
	}
	for _, want := range errors {
		if !strings.Contains(errOutput, want) {
			t.Fatalf("expected error %q: %s", want, errOutput)
		}
	}
}

func TestInit_providerSource(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	configDirName := "init-required-providers"
	copy.CopyDir(testFixturePath(configDirName), filepath.Join(td, configDirName))
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	providerSource, close := newMockProviderSource(t, map[string][]string{
		"test":      []string{"1.2.3", "1.2.4"},
		"test-beta": []string{"1.2.4"},
		"source":    []string{"1.2.2", "1.2.3", "1.2.1"},
	})
	defer close()

	ui := new(cli.MockUi)
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{configDirName}

	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}
	if strings.Contains(ui.OutputWriter.String(), "Terraform has initialized, but configuration upgrades may be needed") {
		t.Fatalf("unexpected \"configuration upgrade\" warning in output")
	}

	cacheDir := m.providerLocalCacheDir()
	gotPackages := cacheDir.AllAvailablePackages()
	wantPackages := map[addrs.Provider][]providercache.CachedProvider{
		addrs.NewDefaultProvider("test"): {
			{
				Provider:   addrs.NewDefaultProvider("test"),
				Version:    getproviders.MustParseVersion("1.2.3"),
				PackageDir: expectedPackageInstallPath("test", "1.2.3", false),
			},
		},
		addrs.NewDefaultProvider("test-beta"): {
			{
				Provider:   addrs.NewDefaultProvider("test-beta"),
				Version:    getproviders.MustParseVersion("1.2.4"),
				PackageDir: expectedPackageInstallPath("test-beta", "1.2.4", false),
			},
		},
		addrs.NewDefaultProvider("source"): {
			{
				Provider:   addrs.NewDefaultProvider("source"),
				Version:    getproviders.MustParseVersion("1.2.3"),
				PackageDir: expectedPackageInstallPath("source", "1.2.3", false),
			},
		},
	}
	if diff := cmp.Diff(wantPackages, gotPackages); diff != "" {
		t.Errorf("wrong cache directory contents after upgrade\n%s", diff)
	}

	inst := m.providerInstaller()
	gotSelected, err := inst.SelectedPackages()
	if err != nil {
		t.Fatalf("failed to get selected packages from installer: %s", err)
	}
	wantSelected := map[addrs.Provider]*providercache.CachedProvider{
		addrs.NewDefaultProvider("test-beta"): {
			Provider:   addrs.NewDefaultProvider("test-beta"),
			Version:    getproviders.MustParseVersion("1.2.4"),
			PackageDir: expectedPackageInstallPath("test-beta", "1.2.4", false),
		},
		addrs.NewDefaultProvider("test"): {
			Provider:   addrs.NewDefaultProvider("test"),
			Version:    getproviders.MustParseVersion("1.2.3"),
			PackageDir: expectedPackageInstallPath("test", "1.2.3", false),
		},
		addrs.NewDefaultProvider("source"): {
			Provider:   addrs.NewDefaultProvider("source"),
			Version:    getproviders.MustParseVersion("1.2.3"),
			PackageDir: expectedPackageInstallPath("source", "1.2.3", false),
		},
	}
	if diff := cmp.Diff(wantSelected, gotSelected); diff != "" {
		t.Errorf("wrong version selections after upgrade\n%s", diff)
	}

	outputStr := ui.OutputWriter.String()
	if want := "Installed hashicorp/test v1.2.3 (verified checksum)"; !strings.Contains(outputStr, want) {
		t.Fatalf("unexpected output: %s\nexpected to include %q", outputStr, want)
	}
}

func TestInit_getUpgradePlugins(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-providers"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	providerSource, close := newMockProviderSource(t, map[string][]string{
		// looking for an exact version
		"exact": []string{"1.2.3"},
		// config requires >= 2.3.3
		"greater-than": []string{"2.3.4", "2.3.3", "2.3.0"},
		// config specifies > 1.0.0 , < 3.0.0
		"between": []string{"3.4.5", "2.3.4", "1.2.3"},
	})
	defer close()

	ui := new(cli.MockUi)
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	installFakeProviderPackages(t, &m, map[string][]string{
		"exact":        []string{"0.0.1"},
		"greater-than": []string{"2.3.3"},
	})

	c := &InitCommand{
		Meta: m,
	}

	args := []string{
		"-upgrade=true",
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("command did not complete successfully:\n%s", ui.ErrorWriter.String())
	}

	cacheDir := m.providerLocalCacheDir()
	gotPackages := cacheDir.AllAvailablePackages()
	wantPackages := map[addrs.Provider][]providercache.CachedProvider{
		// "between" wasn't previously installed at all, so we installed
		// the newest available version that matched the version constraints.
		addrs.NewDefaultProvider("between"): {
			{
				Provider:   addrs.NewDefaultProvider("between"),
				Version:    getproviders.MustParseVersion("2.3.4"),
				PackageDir: expectedPackageInstallPath("between", "2.3.4", false),
			},
		},
		// The existing version of "exact" did not match the version constraints,
		// so we installed what the configuration selected as well.
		addrs.NewDefaultProvider("exact"): {
			{
				Provider:   addrs.NewDefaultProvider("exact"),
				Version:    getproviders.MustParseVersion("1.2.3"),
				PackageDir: expectedPackageInstallPath("exact", "1.2.3", false),
			},
			// Previous version is still there, but not selected
			{
				Provider:   addrs.NewDefaultProvider("exact"),
				Version:    getproviders.MustParseVersion("0.0.1"),
				PackageDir: expectedPackageInstallPath("exact", "0.0.1", false),
			},
		},
		// The existing version of "greater-than" _did_ match the constraints,
		// but a newer version was available and the user specified
		// -upgrade and so we upgraded it anyway.
		addrs.NewDefaultProvider("greater-than"): {
			{
				Provider:   addrs.NewDefaultProvider("greater-than"),
				Version:    getproviders.MustParseVersion("2.3.4"),
				PackageDir: expectedPackageInstallPath("greater-than", "2.3.4", false),
			},
			// Previous version is still there, but not selected
			{
				Provider:   addrs.NewDefaultProvider("greater-than"),
				Version:    getproviders.MustParseVersion("2.3.3"),
				PackageDir: expectedPackageInstallPath("greater-than", "2.3.3", false),
			},
		},
	}
	if diff := cmp.Diff(wantPackages, gotPackages); diff != "" {
		t.Errorf("wrong cache directory contents after upgrade\n%s", diff)
	}

	inst := m.providerInstaller()
	gotSelected, err := inst.SelectedPackages()
	if err != nil {
		t.Fatalf("failed to get selected packages from installer: %s", err)
	}
	wantSelected := map[addrs.Provider]*providercache.CachedProvider{
		addrs.NewDefaultProvider("between"): {
			Provider:   addrs.NewDefaultProvider("between"),
			Version:    getproviders.MustParseVersion("2.3.4"),
			PackageDir: expectedPackageInstallPath("between", "2.3.4", false),
		},
		addrs.NewDefaultProvider("exact"): {
			Provider:   addrs.NewDefaultProvider("exact"),
			Version:    getproviders.MustParseVersion("1.2.3"),
			PackageDir: expectedPackageInstallPath("exact", "1.2.3", false),
		},
		addrs.NewDefaultProvider("greater-than"): {
			Provider:   addrs.NewDefaultProvider("greater-than"),
			Version:    getproviders.MustParseVersion("2.3.4"),
			PackageDir: expectedPackageInstallPath("greater-than", "2.3.4", false),
		},
	}
	if diff := cmp.Diff(wantSelected, gotSelected); diff != "" {
		t.Errorf("wrong version selections after upgrade\n%s", diff)
	}

}

func TestInit_getProviderMissing(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-providers"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	providerSource, close := newMockProviderSource(t, map[string][]string{
		// looking for exact version 1.2.3
		"exact": []string{"1.2.4"},
		// config requires >= 2.3.3
		"greater-than": []string{"2.3.4", "2.3.3", "2.3.0"},
		// config specifies
		"between": []string{"3.4.5", "2.3.4", "1.2.3"},
	})
	defer close()

	ui := new(cli.MockUi)
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{}
	if code := c.Run(args); code == 0 {
		t.Fatalf("expected error, got output: \n%s", ui.OutputWriter.String())
	}

	if !strings.Contains(ui.ErrorWriter.String(), "no available releases match") {
		t.Fatalf("unexpected error output: %s", ui.ErrorWriter)
	}
}

func TestInit_checkRequiredVersion(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-check-required-version"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := cli.NewMockUi()
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{}
	if code := c.Run(args); code != 1 {
		t.Fatalf("got exit status %d; want 1\nstderr:\n%s\n\nstdout:\n%s", code, ui.ErrorWriter.String(), ui.OutputWriter.String())
	}
}

func TestInit_providerLockFile(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-provider-lock-file"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	providerSource, close := newMockProviderSource(t, map[string][]string{
		"test": []string{"1.2.3"},
	})
	defer close()

	ui := new(cli.MockUi)
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	selectionsFile := ".terraform/plugins/selections.json"
	buf, err := ioutil.ReadFile(selectionsFile)
	if err != nil {
		t.Fatalf("failed to read provider selections file %s: %s", selectionsFile, err)
	}
	// The hash in here is for the fake package that newMockProviderSource produces
	// (so it'll change if newMockProviderSource starts producing different contents)
	wantLockFile := strings.TrimSpace(`
{
  "registry.terraform.io/hashicorp/test": {
    "hash": "h1:wlbEC2mChQZ2hhgUhl6SeVLPP7fMqOFUZAQhQ9GIIno=",
    "version": "1.2.3"
  }
}
`)
	if string(buf) != wantLockFile {
		t.Errorf("wrong provider selections file contents\ngot:  %s\nwant: %s", buf, wantLockFile)
	}
}

func TestInit_pluginDirReset(t *testing.T) {
	td := testTempDir(t)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// An empty provider source
	providerSource, close := newMockProviderSource(t, nil)
	defer close()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
			ProviderSource:   providerSource,
		},
	}

	// make our vendor paths
	pluginPath := []string{"a", "b", "c"}
	for _, p := range pluginPath {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// run once and save the -plugin-dir
	args := []string{"-plugin-dir", "a"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter)
	}

	pluginDirs, err := c.loadPluginPath()
	if err != nil {
		t.Fatal(err)
	}

	if len(pluginDirs) != 1 || pluginDirs[0] != "a" {
		t.Fatalf(`expected plugin dir ["a"], got %q`, pluginDirs)
	}

	ui = new(cli.MockUi)
	c = &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
			ProviderSource:   providerSource, // still empty
		},
	}

	// make sure we remove the plugin-dir record
	args = []string{"-plugin-dir="}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter)
	}

	pluginDirs, err = c.loadPluginPath()
	if err != nil {
		t.Fatal(err)
	}

	if len(pluginDirs) != 0 {
		t.Fatalf("expected no plugin dirs got %q", pluginDirs)
	}
}

// Test user-supplied -plugin-dir
func TestInit_pluginDirProviders(t *testing.T) {
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-providers"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// An empty provider source
	providerSource, close := newMockProviderSource(t, nil)
	defer close()

	ui := new(cli.MockUi)
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	// make our vendor paths
	pluginPath := []string{"a", "b", "c"}
	for _, p := range pluginPath {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// We'll put some providers in our plugin dirs. To do this, we'll pretend
	// for a moment that they are provider cache directories just because that
	// allows us to lean on our existing test helper functions to do this.
	for i, def := range [][]string{
		[]string{"exact", "1.2.3"},
		[]string{"greater-than", "2.3.4"},
		[]string{"between", "2.3.4"},
	} {
		name, version := def[0], def[1]
		dir := providercache.NewDir(pluginPath[i])
		installFakeProviderPackagesElsewhere(t, dir, map[string][]string{
			name: []string{version},
		})
	}

	args := []string{
		"-plugin-dir", "a",
		"-plugin-dir", "b",
		"-plugin-dir", "c",
	}
	if code := c.Run(args); code != 0 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter)
	}

	inst := m.providerInstaller()
	gotSelected, err := inst.SelectedPackages()
	if err != nil {
		t.Fatalf("failed to get selected packages from installer: %s", err)
	}
	wantSelected := map[addrs.Provider]*providercache.CachedProvider{
		addrs.NewDefaultProvider("between"): {
			Provider:   addrs.NewDefaultProvider("between"),
			Version:    getproviders.MustParseVersion("2.3.4"),
			PackageDir: expectedPackageInstallPath("between", "2.3.4", false),
		},
		addrs.NewDefaultProvider("exact"): {
			Provider:   addrs.NewDefaultProvider("exact"),
			Version:    getproviders.MustParseVersion("1.2.3"),
			PackageDir: expectedPackageInstallPath("exact", "1.2.3", false),
		},
		addrs.NewDefaultProvider("greater-than"): {
			Provider:   addrs.NewDefaultProvider("greater-than"),
			Version:    getproviders.MustParseVersion("2.3.4"),
			PackageDir: expectedPackageInstallPath("greater-than", "2.3.4", false),
		},
	}
	if diff := cmp.Diff(wantSelected, gotSelected); diff != "" {
		t.Errorf("wrong version selections after upgrade\n%s", diff)
	}

	// -plugin-dir overrides the normal provider source, so it should not have
	// seen any calls at all.
	if calls := providerSource.CallLog(); len(calls) > 0 {
		t.Errorf("unexpected provider source calls (want none)\n%s", spew.Sdump(calls))
	}
}

// Test user-supplied -plugin-dir doesn't allow auto-install
func TestInit_pluginDirProvidersDoesNotGet(t *testing.T) {
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-get-providers"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// Our provider source has a suitable package for "between" available,
	// but we should ignore it because -plugin-dir is set and thus this
	// source is temporarily overridden during install.
	providerSource, close := newMockProviderSource(t, map[string][]string{
		"between": []string{"2.3.4"},
	})
	defer close()

	ui := cli.NewMockUi()
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	// make our vendor paths
	pluginPath := []string{"a", "b"}
	for _, p := range pluginPath {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// We'll put some providers in our plugin dirs. To do this, we'll pretend
	// for a moment that they are provider cache directories just because that
	// allows us to lean on our existing test helper functions to do this.
	for i, def := range [][]string{
		[]string{"exact", "1.2.3"},
		[]string{"greater-than", "2.3.4"},
	} {
		name, version := def[0], def[1]
		dir := providercache.NewDir(pluginPath[i])
		installFakeProviderPackagesElsewhere(t, dir, map[string][]string{
			name: []string{version},
		})
	}

	args := []string{
		"-plugin-dir", "a",
		"-plugin-dir", "b",
	}
	if code := c.Run(args); code == 0 {
		// should have been an error
		t.Fatalf("succeeded; want error\nstdout:\n%s\nstderr\n%s", ui.OutputWriter, ui.ErrorWriter)
	}

	// The error output should mention the "between" provider but should not
	// mention either the "exact" or "greater-than" provider, because the
	// latter two are available via the -plugin-dir directories.
	errStr := ui.ErrorWriter.String()
	if subStr := "hashicorp/between"; !strings.Contains(errStr, subStr) {
		t.Errorf("error output should mention the 'between' provider\nwant substr: %s\ngot:\n%s", subStr, errStr)
	}
	if subStr := "hashicorp/exact"; strings.Contains(errStr, subStr) {
		t.Errorf("error output should not mention the 'exact' provider\ndo not want substr: %s\ngot:\n%s", subStr, errStr)
	}
	if subStr := "hashicorp/greater-than"; strings.Contains(errStr, subStr) {
		t.Errorf("error output should not mention the 'greater-than' provider\ndo not want substr: %s\ngot:\n%s", subStr, errStr)
	}

	if calls := providerSource.CallLog(); len(calls) > 0 {
		t.Errorf("unexpected provider source calls (want none)\n%s", spew.Sdump(calls))
	}
}

// Verify that plugin-dir doesn't prevent discovery of internal providers
func TestInit_pluginDirWithBuiltIn(t *testing.T) {
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-internal"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// An empty provider source
	providerSource, close := newMockProviderSource(t, nil)
	defer close()

	ui := cli.NewMockUi()
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	args := []string{"-plugin-dir", "./"}
	if code := c.Run(args); code != 0 {
		t.Fatalf("error: %s", ui.ErrorWriter)
	}

	outputStr := ui.OutputWriter.String()
	if subStr := "terraform.io/builtin/terraform is built in to Terraform"; !strings.Contains(outputStr, subStr) {
		t.Errorf("output should mention the terraform provider\nwant substr: %s\ngot:\n%s", subStr, outputStr)
	}
}

func TestInit_invalidBuiltInProviders(t *testing.T) {
	// This test fixture includes two invalid provider dependencies:
	// - an implied dependency on terraform.io/builtin/terraform with an
	//   explicit version number, which is not allowed because it's builtin.
	// - an explicit dependency on terraform.io/builtin/nonexist, which does
	//   not exist at all.
	td := tempDir(t)
	copy.CopyDir(testFixturePath("init-internal-invalid"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	// An empty provider source
	providerSource, close := newMockProviderSource(t, nil)
	defer close()

	ui := cli.NewMockUi()
	m := Meta{
		testingOverrides: metaOverridesForProvider(testProvider()),
		Ui:               ui,
		ProviderSource:   providerSource,
	}

	c := &InitCommand{
		Meta: m,
	}

	if code := c.Run(nil); code == 0 {
		t.Fatalf("succeeded, but was expecting error\nstdout:\n%s\nstderr:\n%s", ui.OutputWriter, ui.ErrorWriter)
	}

	errStr := ui.ErrorWriter.String()
	if subStr := "Cannot use terraform.io/builtin/terraform: built-in"; !strings.Contains(errStr, subStr) {
		t.Errorf("error output should mention the terraform provider\nwant substr: %s\ngot:\n%s", subStr, errStr)
	}
	if subStr := "Cannot use terraform.io/builtin/nonexist: this Terraform release"; !strings.Contains(errStr, subStr) {
		t.Errorf("error output should mention the 'nonexist' provider\nwant substr: %s\ngot:\n%s", subStr, errStr)
	}
}

// The module in this test uses terraform 0.11-style syntax. We expect that the
// earlyconfig will succeed but the main loader fail, and return an error that
// indicates that syntax upgrades may be required.
func TestInit_syntaxErrorUpgradeHint(t *testing.T) {
	// Create a temporary working directory that is empty
	td := tempDir(t)

	// This module
	copy.CopyDir(testFixturePath("init-sniff-version-error"), td)
	defer os.RemoveAll(td)
	defer testChdir(t, td)()

	ui := new(cli.MockUi)
	c := &InitCommand{
		Meta: Meta{
			testingOverrides: metaOverridesForProvider(testProvider()),
			Ui:               ui,
		},
	}

	args := []string{}
	if code := c.Run(args); code != 1 {
		t.Fatalf("bad: \n%s", ui.ErrorWriter.String())
	}

	// Check output.
	output := ui.ErrorWriter.String()
	if got, want := output, "If you've recently upgraded to Terraform v0.13 from Terraform\nv0.11, this may be because your configuration uses syntax constructs that are no\nlonger valid"; !strings.Contains(got, want) {
		t.Fatalf("wrong output\ngot:\n%s\n\nwant: message containing %q", got, want)
	}
}

// newMockProviderSource is a helper to succinctly construct a mock provider
// source that contains a set of packages matching the given provider versions
// that are available for installation (from temporary local files).
//
// The caller must call the returned close callback once the source is no
// longer needed, at which point it will clean up all of the temporary files
// and the packages in the source will no longer be available for installation.
//
// Provider addresses must be valid source strings, and passing only the
// provider name will be interpreted as a "default" provider under
// registry.terraform.io/hashicorp. If you need more control over the
// provider addresses, pass a full provider source string.
//
// This function also registers providers as belonging to the current platform,
// to ensure that they will be available to a provider installer operating in
// its default configuration.
//
// In case of any errors while constructing the source, this function will
// abort the current test using the given testing.T. Therefore a caller can
// assume that if this function returns then the result is valid and ready
// to use.
func newMockProviderSource(t *testing.T, availableProviderVersions map[string][]string) (source *getproviders.MockSource, close func()) {
	t.Helper()
	var packages []getproviders.PackageMeta
	var closes []func()
	close = func() {
		for _, f := range closes {
			f()
		}
	}
	for source, versions := range availableProviderVersions {
		addr := addrs.MustParseProviderSourceString(source)
		for _, versionStr := range versions {
			version, err := getproviders.ParseVersion(versionStr)
			if err != nil {
				close()
				t.Fatalf("failed to parse %q as a version number for %q: %s", versionStr, addr.ForDisplay(), err)
			}
			meta, close, err := getproviders.FakeInstallablePackageMeta(addr, version, getproviders.VersionList{getproviders.MustParseVersion("5.0")}, getproviders.CurrentPlatform)
			if err != nil {
				close()
				t.Fatalf("failed to prepare fake package for %s %s: %s", addr.ForDisplay(), versionStr, err)
			}
			closes = append(closes, close)
			packages = append(packages, meta)
		}
	}

	return getproviders.NewMockSource(packages, nil), close
}

// installFakeProviderPackages installs a fake package for the given provider
// names (interpreted as a "default" provider address) and versions into the
// local plugin cache for the given "meta".
//
// Any test using this must be using testChdir or some similar mechanism to
// make sure that it isn't writing directly into a test fixture or source
// directory within the codebase.
//
// If a requested package cannot be installed for some reason, this function
// will abort the test using the given testing.T. Therefore if this function
// returns the caller can assume that the requested providers have been
// installed.
func installFakeProviderPackages(t *testing.T, meta *Meta, providerVersions map[string][]string) {
	t.Helper()

	cacheDir := meta.providerLocalCacheDir()
	installFakeProviderPackagesElsewhere(t, cacheDir, providerVersions)
}

// installFakeProviderPackagesElsewhere is a variant of installFakeProviderPackages
// that will install packages into the given provider cache directory, rather
// than forcing the use of the local cache of the current "Meta".
func installFakeProviderPackagesElsewhere(t *testing.T, cacheDir *providercache.Dir, providerVersions map[string][]string) {
	t.Helper()

	// It can be hard to spot the mistake of forgetting to run testChdir before
	// modifying the working directory, so we'll use a simple heuristic here
	// to try to detect that mistake and make a noisy error about it instead.
	wd, err := os.Getwd()
	if err == nil {
		wd = filepath.Clean(wd)
		// If the directory we're in is named "command" or if we're under a
		// directory named "testdata" then we'll assume a mistake and generate
		// an error. This will cause the test to fail but won't block it from
		// running.
		if filepath.Base(wd) == "command" || filepath.Base(wd) == "testdata" || strings.Contains(filepath.ToSlash(wd), "/testdata/") {
			t.Errorf("installFakeProviderPackage may be used only by tests that switch to a temporary working directory, e.g. using testChdir")
		}
	}

	for name, versions := range providerVersions {
		addr := addrs.NewDefaultProvider(name)
		for _, versionStr := range versions {
			version, err := getproviders.ParseVersion(versionStr)
			if err != nil {
				t.Fatalf("failed to parse %q as a version number for %q: %s", versionStr, name, err)
			}
			meta, close, err := getproviders.FakeInstallablePackageMeta(addr, version, getproviders.VersionList{getproviders.MustParseVersion("5.0")}, getproviders.CurrentPlatform)
			// We're going to install all these fake packages before we return,
			// so we don't need to preserve them afterwards.
			defer close()
			if err != nil {
				t.Fatalf("failed to prepare fake package for %s %s: %s", name, versionStr, err)
			}
			_, err = cacheDir.InstallPackage(context.Background(), meta)
			if err != nil {
				t.Fatalf("failed to install fake package for %s %s: %s", name, versionStr, err)
			}
		}
	}
}

// expectedPackageInstallPath is a companion to installFakeProviderPackages
// that returns the path where the provider with the given name and version
// would be installed and, relatedly, where the installer will expect to
// find an already-installed version.
//
// Just as with installFakeProviderPackages, this function is a shortcut helper
// for "default-namespaced" providers as we commonly use in tests. If you need
// more control over the provider addresses, use functions of the underlying
// getproviders and providercache packages instead.
//
// The result always uses forward slashes, even on Windows, for consistency
// with how the getproviders and providercache packages build paths.
func expectedPackageInstallPath(name, version string, exe bool) string {
	platform := getproviders.CurrentPlatform
	baseDir := ".terraform/plugins"
	if exe {
		p := fmt.Sprintf("registry.terraform.io/hashicorp/%s/%s/%s/terraform-provider-%s_%s", name, version, platform, name, version)
		if platform.OS == "windows" {
			p += ".exe"
		}
		return filepath.ToSlash(filepath.Join(baseDir, p))
	}
	return filepath.ToSlash(filepath.Join(
		baseDir, fmt.Sprintf("registry.terraform.io/hashicorp/%s/%s/%s", name, version, platform),
	))
}
