#!/usr/bin/env python3

import unittest

import agent_hook


class GitVerdictTest(unittest.TestCase):
    """Verifies which git commands the guard refuses, on a dirty and on a clean checkout."""

    CASES = [
        # (name, script, refused when dirty, refused when clean)
        ("whole-tree add", "git add -A", True, True),
        ("add of the current directory", "git add .", True, True),
        ("pathless update add", "git add -u", True, True),
        ("add of explicit paths", "git add a.go b/c.go", False, False),
        ("commit that skips hooks", "git commit -nm msg", True, True),
        ("commit whose message starts with n", "git commit -m -nope", False, False),
        ("merge that skips hooks", "git -C /tmp merge --no-verify topic", True, True),
        ("stash push", "git stash push -m wip", True, True),
        ("stash list", "git stash list", False, False),
        ("hard reset", "git reset --hard origin/main", True, False),
        ("checkout of paths", "git checkout -- a.go", True, False),
        ("checkout of a new branch", "git checkout -b topic origin/main", False, False),
        ("restore of the worktree", "git restore a.go", True, False),
        ("restore of the index only", "git restore --staged a.go", False, False),
        ("forced clean", "git clean -fdx", True, False),
        ("dry-run clean", "git clean -fn", False, False),
        ("git call chained after another command", "make && git add -A", True, True),
        ("git call quoted as data", 'echo "git add -A"', False, False),
        ("git call inside a here-document", "cat <<'EOF' >notes\ngit add -A\nEOF\ngit status", False, False),
    ]

    def test_git_verdict(self):
        for name, script, when_dirty, when_clean in self.CASES:
            for dirty, refused in ((True, when_dirty), (False, when_clean)):
                with self.subTest(name, dirty=dirty):
                    verdicts = [agent_hook.git_verdict(args, lambda: dirty) for args, _ in agent_hook.git_calls(script, "/")]
                    self.assertEqual(any(verdicts), refused)

    def test_git_calls_follow_cd_and_dash_c(self):
        """Verifies that a git call runs in the directory a preceding cd and its own -C select."""
        calls = agent_hook.git_calls("cd /repo/sub && git -C ../other status", "/")
        self.assertEqual(calls, [(["status"], "/repo/other")])


class EditedPathsTest(unittest.TestCase):
    """Verifies that edit payloads of both clients name the files they write."""

    def test_claude_edit(self):
        payload = {"tool_name": "Edit", "cwd": "/repo", "tool_input": {"file_path": "/repo/a.go"}}
        self.assertEqual(agent_hook.edited_paths(payload), ["/repo/a.go"])

    def test_codex_patch(self):
        patch = (
            "*** Begin Patch\n*** Add File: new.go\n+package x\n*** Update File: old.go\n*** Move to: moved.go\n"
            "@@\n-a\n+b\n*** Delete File: gone.go\n*** End Patch\n"
        )
        payload = {"tool_name": "apply_patch", "cwd": "/repo", "tool_input": {"command": patch}}
        self.assertEqual(
            agent_hook.edited_paths(payload),
            ["/repo/new.go", "/repo/old.go", "/repo/moved.go", "/repo/gone.go"],
        )
        self.assertNotIn("/repo/gone.go", agent_hook.edited_paths(payload, deleted=False))


if __name__ == "__main__":
    unittest.main()
