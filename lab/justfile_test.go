package lab_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabRecipePreservesArguments(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("just is not installed")
	}

	binDirectory := t.TempDir()
	fakeGo := filepath.Join(binDirectory, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}

	args := []string{
		"--justfile", "../Justfile", "lab",
		"exec", "--", "printf", "value with spaces; echo HOST_INJECTION",
		"$HOME", "single'quote",
	}
	command := exec.Command(just, args...)
	command.Env = append(os.Environ(), "PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run lab recipe: %v\n%s", err, output)
	}

	want := strings.Join([]string{
		"<run>",
		"<./cmd/yanet-lab>",
		"<exec>",
		"<-->",
		"<printf>",
		"<value with spaces; echo HOST_INJECTION>",
		"<$HOME>",
		"<single'quote>",
		"",
	}, "\n")
	if string(output) != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

// Test_DockerRecipe_WorktreeMetadata verifies that relocated gitfiles use
// read-only absolute metadata paths and temporary mounts are always removed.
func Test_DockerRecipe_WorktreeMetadata(t *testing.T) {
	just, err := exec.LookPath("just")
	require.NoError(t, err, "just is required to verify Docker recipe arguments")
	for _, testCase := range []struct {
		name       string
		linked     bool
		relative   bool
		dockerExit string
	}{
		{name: "ordinary checkout", dockerExit: "0"},
		{name: "absolute worktree gitfile", linked: true, dockerExit: "0"},
		{name: "relative worktree gitfile", linked: true, relative: true, dockerExit: "0"},
		{name: "failed Docker invocation removes temporary gitfile", linked: true, dockerExit: "1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			root := filepath.Join(directory, "worktree with spaces")
			commonDirectory := filepath.Join(directory, "main checkout", ".git")
			gitDirectory := filepath.Join(commonDirectory, "worktrees", "example")
			binDirectory := filepath.Join(directory, "bin")
			for _, path := range []string{root, gitDirectory, binDirectory} {
				require.NoError(t, os.MkdirAll(path, 0o755))
			}
			var originalGitfile []byte
			if testCase.linked {
				gitPath := gitDirectory
				if testCase.relative {
					gitPath, err = filepath.Rel(root, gitDirectory)
					require.NoError(t, err)
				}
				originalGitfile = []byte("gitdir: " + gitPath + "\n")
				require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), originalGitfile, 0o600))
			} else {
				require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			}
			fakeGit := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$GIT_CALLS"
[ "$1" = -C ]
[ "$2" = "$FIXTURE_ROOT" ]
[ "$3" = rev-parse ]
case "$4" in
    --path-format=absolute)
        [ "$#" -eq 5 ]
        [ "$5" = --git-common-dir ]
        printf '%s\n' "$FIXTURE_COMMON_DIR" ;;
    --absolute-git-dir)
        [ "$#" -eq 4 ]
        printf '%s\n' "$FIXTURE_GIT_DIR" ;;
    *) exit 2 ;;
esac
`
			fakeDocker := `#!/bin/sh
set -eu
printf '%s\0' "$@" > "$DOCKER_ARGUMENTS"
for argument do
    case "$argument" in
        *:/yanet2/.git:ro)
            gitfile="${argument%:/yanet2/.git:ro}"
            printf '%s' "$gitfile" > "$GITFILE_PATH"
            cat "$gitfile" > "$GITFILE_CONTENT"
            ;;
    esac
done
exit "$DOCKER_EXIT_CODE"
`
			require.NoError(t, os.WriteFile(filepath.Join(binDirectory, "git"), []byte(fakeGit), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(binDirectory, "docker"), []byte(fakeDocker), 0o755))
			command := exec.CommandContext(t.Context(), just,
				"--justfile", "../Justfile", "--set", "ROOT_DIR", root,
				"_docker_run", "-i", "git rev-parse HEAD",
			)
			command.Env = append(os.Environ(),
				"PATH="+binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FIXTURE_ROOT="+root,
				"FIXTURE_COMMON_DIR="+commonDirectory,
				"FIXTURE_GIT_DIR="+gitDirectory,
				"GIT_CALLS="+filepath.Join(directory, "git-calls"),
				"DOCKER_ARGUMENTS="+filepath.Join(directory, "docker-arguments"),
				"GITFILE_PATH="+filepath.Join(directory, "gitfile-path"),
				"GITFILE_CONTENT="+filepath.Join(directory, "gitfile-content"),
				"DOCKER_EXIT_CODE="+testCase.dockerExit,
			)
			output, runErr := command.CombinedOutput()
			if testCase.dockerExit == "0" {
				require.NoError(t, runErr, "%s", output)
			} else {
				require.Error(t, runErr, "%s", output)
			}
			data, err := os.ReadFile(filepath.Join(directory, "docker-arguments"))
			require.NoError(t, err)
			arguments := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
			require.Contains(t, arguments, root+":/yanet2")
			if testCase.linked {
				require.Contains(t, arguments, commonDirectory+":"+commonDirectory+":ro")
				content, err := os.ReadFile(filepath.Join(directory, "gitfile-content"))
				require.NoError(t, err)
				require.Equal(t, "gitdir: "+gitDirectory+"\n", string(content))
				path, err := os.ReadFile(filepath.Join(directory, "gitfile-path"))
				require.NoError(t, err)
				_, err = os.Stat(string(path))
				require.ErrorIs(t, err, os.ErrNotExist)
				content, err = os.ReadFile(filepath.Join(root, ".git"))
				require.NoError(t, err)
				require.Equal(t, originalGitfile, content)
			} else {
				require.NotContains(t, arguments, commonDirectory+":"+commonDirectory+":ro")
				_, err := os.Stat(filepath.Join(directory, "git-calls"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}
