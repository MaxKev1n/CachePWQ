#!/usr/bin/env python3
"""
update_repo.py

Commit and optionally push changes in the current repository,
ensuring only valid .go files (no *_test.go) are committed.

Usage:
    python scripts/update_repo.py -m "Fix cuPTW queue handling" --push
    python scripts/update_repo.py -m "Refactor MMUImpl" --prefix "[refactor]" --type "Optimization" --push
"""

import os
import subprocess
import sys
import datetime
import argparse
import re


def run(cmd, check=True, capture_output=False):
    """Run a shell command."""
    print(f"$ {' '.join(cmd)}")
    result = subprocess.run(
        cmd,
        capture_output=capture_output,
        text=True,
    )
    if check and result.returncode != 0:
        if result.stderr:
            print(result.stderr)
        sys.exit(result.returncode)
    return result


def get_git_config(field):
    """Get a git config field, e.g., user.name or user.email"""
    result = subprocess.run(
        ["git", "config", "--get", field],
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def get_staged_files():
    """Return a list of staged file paths (relative paths)."""
    result = subprocess.run(
        ["git", "diff", "--cached", "--name-only"],
        capture_output=True,
        text=True,
        check=True,
    )
    files = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    return files


def validate_files(file_list):
    """Ensure only .go files are committed and exclude *_test.go files."""
    invalid_files = []
    forbidden_tests = []
    allowed = re.compile(r".+\\.go$")

    for f in file_list:
        if "_test" in f:
            forbidden_tests.append(f)
        elif not allowed.match(f):
            invalid_files.append(f)

    return invalid_files, forbidden_tests


def main():
    parser = argparse.ArgumentParser(
        description="Commit and optionally push changes in the current repository."
    )
    parser.add_argument("-m", "--message", required=True, help="Commit message.")
    parser.add_argument(
        "--prefix",
        default="[auto-sync]",
        help="Prefix tag for commit message, e.g., [fix], [refactor], [infra]. Default: [auto-sync]",
    )
    parser.add_argument(
        "--type",
        default="Local repository update",
        help='Specify commit type (e.g., "Optimization", "Tooling", "Experiment"). Default: "Local repository update".',
    )
    parser.add_argument("--push", action="store_true", help="Push to origin/main.")
    parser.add_argument(
        "--ignore-warnings",
        action="store_true",
        help="Ignore file validation warnings and proceed with commit.",
    )
    parser.add_argument(
        "--force", action="store_true", help="Force push to the remote repository."
    )
    args = parser.parse_args()

    root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))

    # === Ensure we are in a Git repo ===
    if not os.path.isdir(os.path.join(root, ".git")):
        print("❌ Error: current directory is not a Git repository.")
        sys.exit(1)

    # === Get author info ===
    author_name = get_git_config("user.name")
    author_email = get_git_config("user.email")

    if not author_name or not author_email:
        print("⚠️  Missing author info in git config.")
        print("👉 Run the following to set it up:")
        print('   git config --global user.name "Your Name"')
        print('   git config --global user.email "you@example.com"')
        sys.exit(1)

    # === Stage all changes ===
    print("\n📦 Staging changes...")

    diff_check = subprocess.run(["git", "diff", "--cached", "--quiet"])
    if diff_check.returncode == 0:
        print("✅ No changes to commit.")
        sys.exit(0)

    # === Validate staged files ===
    staged_files = get_staged_files()
    invalid_files, forbidden_tests = validate_files(staged_files)

    if not args.ignore_warnings:
        if invalid_files or forbidden_tests:
            print("❌ Commit rejected due to invalid files:")
            if invalid_files:
                print("  - Non-Go files detected:")
                for f in invalid_files:
                    print(f"    ✗ {f}")
            if forbidden_tests:
                print("  - Test files not allowed:")
                for f in forbidden_tests:
                    print(f"    ✗ {f}")
            print(
                "\n💡 Only .go files are allowed, and filenames must not contain '_test'."
            )
            sys.exit(1)
    else:
        print("⚠️  Warnings ignored as per --ignore-warnings flag.")

    # === Commit ===
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    prefix = args.prefix.strip()
    commit_type = args.type.strip()
    commit_msg = f"""{prefix} {args.message}

Author: {author_name} <{author_email}>
Date: {now}
Type: {commit_type}
Note: Generated via scripts/update_repo.py
"""
    run(["git", "commit", "-m", commit_msg])

    # === Optional push ===
    if args.push:
        print("\n🚀 Pushing to origin/main...")
        push_cmd = ["git", "push", "origin", "main"]
        if args.force:
            push_cmd.append("--force")
        run(push_cmd)
    else:
        print("⚠️  Skipping push (use --push to push to remote).")

    print("\n✅ Done! Repository changes committed successfully.")


if __name__ == "__main__":
    main()
