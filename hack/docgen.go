
// Package docgen provides functionality for generating osdctl documentation
package docgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openshift/osdctl/cmd"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	DefaultStateFile  = "cmd_state.json"
	DefaultCmdPath    = "./cmd"
	DefaultDocsDir    = "./docs"
	DefaultReadmeFile = "README.md"
	DefaultOutFile    = "README_mock.md"
)

// CommandCategory represents a categorized command
type CommandCategory struct {
	Name        string
	Description string
	Category    string
}

// fileState tracks the state of command files
type fileState struct {
	mu    sync.RWMutex
	state map[string]string
}

// Options holds the configuration for the documentation generator
type Options struct {
	// CmdPath is the path to the cmd directory
	CmdPath string
	// DocsDir is the output directory for generated docs
	DocsDir string
	// ReadmeFile is the source README file
	ReadmeFile string
	// OutputFile is the file to write the updated README to
	OutputFile string
	// StateFile is the path to the state file
	StateFile string
	// Force regeneration even if no changes detected
	Force bool
	// VerifyOnly checks if docs are up to date without modifying
	VerifyOnly bool
	// Logger for output
	Logger *log.Logger
	// IOStreams for command initialization
	IOStreams genericclioptions.IOStreams
}

// NewDefaultOptions returns a new Options with default values
func NewDefaultOptions() *Options {
	return &Options{
		CmdPath:    DefaultCmdPath,
		DocsDir:    DefaultDocsDir,
		ReadmeFile: DefaultReadmeFile,
		OutputFile: DefaultOutFile,
		StateFile:  DefaultStateFile,
		Force:      false,
		VerifyOnly: false,
		Logger:     log.New(os.Stdout, "", log.LstdFlags),
		IOStreams: genericclioptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
}

// newFileState creates a new file state tracker
func newFileState() *fileState {
	return &fileState{
		state: make(map[string]string),
	}
}

// hashFile computes a SHA-256 hash of a file
func hashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", errors.Wrap(err, "opening file")
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", errors.Wrap(err, "computing hash")
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// storeCurrentState computes hashes of all Go files in the cmd directory
func storeCurrentState(cmdPath string) (*fileState, error) {
	state := newFileState()

	err := filepath.Walk(cmdPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return errors.Wrap(err, "walking path")
		}

		if !info.IsDir() && filepath.Ext(path) == ".go" {
			hash, err := hashFile(path)
			if err != nil {
				return errors.Wrapf(err, "hashing file %s", path)
			}

			relativePath, err := filepath.Rel(cmdPath, path)
			if err != nil {
				return errors.Wrap(err, "getting relative path")
			}

			state.mu.Lock()
			state.state[relativePath] = hash
			state.mu.Unlock()
		}
		return nil
	})

	if err != nil {
		return nil, errors.Wrap(err, "scanning directory")
	}

	return state, nil
}

// writeStateToFile saves the current state to a JSON file
func writeStateToFile(state *fileState, stateFilePath string) error {
	state.mu.RLock()
	defer state.mu.RUnlock()

	file, err := os.Create(stateFilePath)
	if err != nil {
		return errors.Wrap(err, "creating state file")
	}
	defer file.Close()

	return json.NewEncoder(file).Encode(state.state)
}

// readStoredState reads the previous state from a JSON file
func readStoredState(stateFilePath string) (*fileState, error) {
	state := newFileState()

	file, err := os.Open(stateFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, errors.Wrap(err, "opening state file")
	}
	defer file.Close()

	state.mu.Lock()
	defer state.mu.Unlock()

	if err := json.NewDecoder(file).Decode(&state.state); err != nil {
		return nil, errors.Wrap(err, "decoding state file")
	}

	return state, nil
}

// detectChanges checks if any files have changed since the last run
func detectChanges(previous, current *fileState) bool {
	if len(previous.state) == 0 {
		return true
	}

	previous.mu.RLock()
	defer previous.mu.RUnlock()

	current.mu.RLock()
	defer current.mu.RUnlock()

	for path, newHash := range current.state {
		if oldHash, exists := previous.state[path]; !exists || oldHash != newHash {
			return true
		}
	}

	return false
}

// categorizeCommand determines the command category based on its hierarchy
func categorizeCommand(cmd *cobra.Command) string {
	if cmd == nil {
		return "General Commands"
	}

	parent := cmd.Parent()
	if parent != nil && parent.Name() != "osdctl" {
		return strings.Title(parent.Name()) + " Commands"
	}
	return "General Commands"
}

// extractCommands builds a map of all commands and their categories
func extractCommands(cmd *cobra.Command) map[string]CommandCategory {
	commands := make(map[string]CommandCategory)

	if cmd.Name() != "" && !cmd.HasParent() {
		commands[cmd.Name()] = CommandCategory{
			Name:        cmd.Name(),
			Description: cmd.Short,
			Category:    categorizeCommand(cmd),
		}
	}

	for _, subCmd := range cmd.Commands() {
		if !subCmd.Hidden && subCmd.Deprecated == "" {
			commands[subCmd.Name()] = CommandCategory{
				Name:        subCmd.Name(),
				Description: subCmd.Short,
				Category:    categorizeCommand(subCmd),
			}
			// Recursively get subcommands
			for k, v := range extractCommands(subCmd) {
				commands[k] = v
			}
		}
	}

	return commands
}

// appendCommandsToReadme adds command documentation to the README
func appendCommandsToReadme(sourceFile, destFile string, newCommands map[string]CommandCategory) error {
	// Read original README content
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		return errors.Wrap(err, "reading source README file")
	}

	// Create output buffer starting with existing content
	var output strings.Builder
	output.Write(content)

	// Ensure there's a newline before we start appending
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		output.WriteString("\n")
	}

	// Group commands by category
	categorizedCommands := make(map[string][]CommandCategory)
	for _, cmd := range newCommands {
		category := cmd.Category
		if category == "" {
			category = "General Commands"
		}
		categorizedCommands[category] = append(categorizedCommands[category], cmd)
	}

	// Append new command documentation section
	output.WriteString("\n## Command Reference\n\n")
	output.WriteString("This section provides a comprehensive list of all available osdctl commands, organized by category.\n\n")

	// Write commands by category
	for category, commands := range categorizedCommands {
		output.WriteString(fmt.Sprintf("### %s\n\n", category))
		for _, cmd := range commands {
			output.WriteString(fmt.Sprintf("* `%s` - %s\n", cmd.Name, cmd.Description))
		}
		output.WriteString("\n")
	}

	// Write the updated content to the destination file
	return os.WriteFile(destFile, []byte(output.String()), 0644)
}

// verifyDocumentation checks if documentation is up to date
func verifyDocumentation(opts *Options) (bool, error) {
	// Read previous state
	previousState, err := readStoredState(opts.StateFile)
	if err != nil {
		return false, errors.Wrap(err, "reading previous state")
	}

	// Get current state
	currentState, err := storeCurrentState(opts.CmdPath)
	if err != nil {
		return false, errors.Wrap(err, "scanning cmd directory")
	}

	// Check for changes
	return !detectChanges(previousState, currentState), nil
}

// GenerateDocs generates the documentation for osdctl commands
func GenerateDocs(opts *Options) error {
	if opts == nil {
		opts = NewDefaultOptions()
	}

	// Ensure directories exist
	if err := os.MkdirAll(opts.DocsDir, 0755); err != nil {
		return errors.Wrap(err, "creating docs directory")
	}

	if _, err := os.Stat(opts.CmdPath); os.IsNotExist(err) {
		return errors.Errorf("cmd directory '%s' does not exist", opts.CmdPath)
	}

	// Check if original README exists
	if _, err := os.Stat(opts.ReadmeFile); os.IsNotExist(err) {
		return errors.Errorf("original README file '%s' does not exist", opts.ReadmeFile)
	}

	// Read previous state
	previousState, err := readStoredState(opts.StateFile)
	if err != nil {
		return errors.Wrap(err, "reading previous state")
	}

	// Get current state
	currentState, err := storeCurrentState(opts.CmdPath)
	if err != nil {
		return errors.Wrap(err, "scanning cmd directory")
	}

	// If verify-only mode, just check if documentation would change
	if opts.VerifyOnly {
		isUpToDate := !detectChanges(previousState, currentState)
		if isUpToDate {
			opts.Logger.Println("✅ Documentation is up to date")
			return nil
		} else {
			return errors.New("documentation is out of date, please run 'make generate-docs' to update")
		}
	}

	// Check for changes and generate docs if needed
	if opts.Force || detectChanges(previousState, currentState) {
		opts.Logger.Println("🔄 Changes detected! Generating documentation...")

		// Initialize root command
		rootCmd := cmd.NewCmdRoot(opts.IOStreams)

		// Extract all commands and their information
		commands := extractCommands(rootCmd)

		// Generate markdown documentation for all commands
		if err := doc.GenMarkdownTree(rootCmd, opts.DocsDir); err != nil {
			return errors.Wrap(err, "generating command documentation")
		}

		// Update the README with command information
		if err := appendCommandsToReadme(opts.ReadmeFile, opts.OutputFile, commands); err != nil {
			return errors.Wrap(err, "creating updated README")
		}

		// Save the current state to avoid regenerating docs unnecessarily
		if err := writeStateToFile(currentState, opts.StateFile); err != nil {
			return errors.Wrap(err, "saving state")
		}

		opts.Logger.Printf("✅ Documentation successfully generated in %s and %s created", opts.DocsDir, opts.OutputFile)
	} else {
		opts.Logger.Println("✅ No changes detected, skipping documentation update.")
	}

	return nil
}

// Command returns a cobra command that can be used to generate documentation
func Command() *cobra.Command {
	opts := NewDefaultOptions()
	cmd := &cobra.Command{
		Use:   "docgen",
		Short: "Generate osdctl documentation",
		Long:  "Generate markdown documentation for osdctl commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return GenerateDocs(opts)
		},
	}

	// Add flags
	cmd.Flags().StringVar(&opts.CmdPath, "cmd-path", opts.CmdPath, "Path to the cmd directory")
	cmd.Flags().StringVar(&opts.DocsDir, "docs-dir", opts.DocsDir, "Path to the docs output directory")
	cmd.Flags().StringVar(&opts.ReadmeFile, "readme", opts.ReadmeFile, "Path to the source README file")
	cmd.Flags().StringVar(&opts.OutputFile, "output", opts.OutputFile, "Path to the output README file")
	cmd.Flags().StringVar(&opts.StateFile, "state-file", opts.StateFile, "Path to the state file")
	cmd.Flags().BoolVar(&opts.Force, "force", opts.Force, "Force generation even if no changes detected")
	cmd.Flags().BoolVar(&opts.VerifyOnly, "verify-only", opts.VerifyOnly, "Only verify documentation is up to date without generating")

	return cmd
}

// Main function that can be called directly from a main.go file
func Main() {
	cmd := Command()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
