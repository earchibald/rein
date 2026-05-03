#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/bootstrap-rein-demo.sh [options]

Create or reuse the lightweight Rust rein-demo repository and seed the matching
rein training project/issues into a running daemon instance.

Options:
  --repo-dir <path>              Local demo repository path.
                                 Default: /Users/earchibald/Projects/rein-demo
  --state-home <path>            XDG state home used by rein.
                                 Default: $XDG_STATE_HOME or ~/.local/state
  --instance <name>              Rein instance to seed. Default: demo
  --rein-bin <path>              Rein binary path. Default: ./bin/rein
  --reset-repo                   Remove and recreate the local demo repository.
  --github                       Create or reuse the matching GitHub repository.
  --github-owner <owner>         GitHub owner. Default: active gh account
  --github-visibility <value>    private or public. Default: private
  -h, --help                     Show this help text.

Notes:
  - The target rein daemon must already be running for the selected instance.
  - The seeded issue set (IDs assigned by the daemon, e.g. RD-1..RD-5):
      scaffold the project with an agent prompt
      replace the hello-world print with a greet helper
      accept a name argument
      add a README quickstart
      add a unit test for the greeting helper
EOF
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

repo_dir="/Users/earchibald/Projects/rein-demo"
state_home="${XDG_STATE_HOME:-$HOME/.local/state}"
instance="demo"
rein_bin="$repo_root/bin/rein"
reset_repo=false
publish_github=false
github_owner=""
github_visibility="private"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir)
      repo_dir="$2"
      shift 2
      ;;
    --state-home)
      state_home="$2"
      shift 2
      ;;
    --instance)
      instance="$2"
      shift 2
      ;;
    --rein-bin)
      rein_bin="$2"
      shift 2
      ;;
    --reset-repo)
      reset_repo=true
      shift
      ;;
    --github)
      publish_github=true
      shift
      ;;
    --github-owner)
      github_owner="$2"
      shift 2
      ;;
    --github-visibility)
      github_visibility="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$github_visibility" in
  private|public) ;;
  *)
    echo "github visibility must be private or public" >&2
    exit 2
    ;;
esac

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 1
  fi
}

run_rein() {
  XDG_STATE_HOME="$state_home" "$rein_bin" --instance "$instance" "$@"
}

repo_name=$(basename "$repo_dir")
project_id="rein-demo"

ensure_rein_ready() {
  if [[ ! -x "$rein_bin" ]]; then
    echo "rein binary not found at $rein_bin; build it first with: go build -o bin/rein ./cmd/rein" >&2
    exit 1
  fi
  if ! run_rein doctor >/dev/null 2>&1; then
    echo "rein daemon for instance '$instance' is not reachable under XDG_STATE_HOME=$state_home" >&2
    echo "start it first, for example:" >&2
    echo "  XDG_STATE_HOME=\"$state_home\" \"$rein_bin\" --instance \"$instance\" daemon serve" >&2
    exit 1
  fi
}

ensure_local_repo() {
  if [[ "$reset_repo" == true && -e "$repo_dir" ]]; then
    rm -rf "$repo_dir"
  fi

  if [[ -d "$repo_dir/.git" ]]; then
    return
  fi

  if [[ -e "$repo_dir" ]]; then
    echo "repo path exists but is not a git repository: $repo_dir" >&2
    exit 1
  fi

  mkdir -p "$(dirname "$repo_dir")"

  if [[ "$publish_github" == true ]]; then
    require_command gh
    if [[ -z "$github_owner" ]]; then
      github_owner=$(gh api user --jq .login)
    fi
    if gh repo view "$github_owner/$repo_name" >/dev/null 2>&1; then
      gh repo clone "$github_owner/$repo_name" "$repo_dir"
      return
    fi
  fi

  require_command cargo
  cargo new --vcs git --bin "$repo_dir"
}

ensure_initial_commit() {
  if git -C "$repo_dir" rev-parse --verify HEAD >/dev/null 2>&1; then
    return
  fi
  git -C "$repo_dir" add .
  git -C "$repo_dir" commit -m "Initial cargo scaffold"
}

ensure_github_repo() {
  if [[ "$publish_github" != true ]]; then
    return
  fi

  require_command gh
  if [[ -z "$github_owner" ]]; then
    github_owner=$(gh api user --jq .login)
  fi

  local full_repo="$github_owner/$repo_name"

  if gh repo view "$full_repo" >/dev/null 2>&1; then
    if ! git -C "$repo_dir" remote get-url origin >/dev/null 2>&1; then
      git -C "$repo_dir" remote add origin "https://github.com/$full_repo.git"
    fi
    return
  fi

  ensure_initial_commit
  gh repo create "$full_repo" "--$github_visibility" \
    --description "Lightweight repeatable rein operator training demo" \
    --source "$repo_dir" \
    --remote origin \
    --push
}

upsert_project() {
  local payload="$1"
  if run_rein project get --id "$project_id" >/dev/null 2>&1; then
    run_rein project update --project "$payload" >/dev/null
  else
    run_rein project create --project "$payload" >/dev/null
  fi
}

# create_issue_if_absent creates an issue only when no issue with that title
# already exists in the project. IDs are assigned by the daemon (auto-generated
# from the project's issue_prefix). On a clean seed they will be RD-1..RD-5.
create_issue_if_absent() {
  local title="$1"
  local payload="$2"
  local existing
  existing=$(run_rein issue list --project_id "$project_id" 2>/dev/null || true)
  if echo "$existing" | grep -qF "$title"; then
    return
  fi
  run_rein issue create --issue "$payload" >/dev/null
}

seed_rein_project() {
  local project_payload
  project_payload=$(cat <<EOF
{"id":"rein-demo","slug":"rein-demo","displayName":"rein-demo","summary":"Repeatable first-run training project backed by a tiny Rust binary repo.","status":"PROJECT_STATUS_ACTIVE","repoPath":"$repo_dir"}
EOF
)
  upsert_project "$project_payload"

  create_issue_if_absent "Scaffold the demo project with an agent prompt" "$(cat <<'EOF'
{"projectId":"rein-demo","title":"Scaffold the demo project with an agent prompt","summary":"Seed the repeatable training loop for the lightweight Rust demo repository.","priority":"ISSUE_PRIORITY_HIGH","assignee":"operator"}
EOF
)"
  create_issue_if_absent "Replace the hello-world print with a reusable greet helper" "$(cat <<'EOF'
{"projectId":"rein-demo","title":"Replace the hello-world print with a reusable greet helper","summary":"Refactor the default cargo scaffold so greeting logic is reusable and ready for tests.","priority":"ISSUE_PRIORITY_MEDIUM","assignee":"operator"}
EOF
)"
  create_issue_if_absent "Accept a name argument for a personalized greeting" "$(cat <<'EOF'
{"projectId":"rein-demo","title":"Accept a name argument for a personalized greeting","summary":"Extend the tiny Rust binary to accept one name argument without adding heavy dependencies.","priority":"ISSUE_PRIORITY_MEDIUM","assignee":"operator"}
EOF
)"
  create_issue_if_absent "Add a README quickstart for build and run" "$(cat <<'EOF'
{"projectId":"rein-demo","title":"Add a README quickstart for build and run","summary":"Document how to build and run the tiny Rust demo so the training repo has an obvious landing page.","priority":"ISSUE_PRIORITY_MEDIUM","assignee":"operator"}
EOF
)"
  create_issue_if_absent "Add a unit test for the greeting helper" "$(cat <<'EOF'
{"projectId":"rein-demo","title":"Add a unit test for the greeting helper","summary":"Introduce one small automated test so the training loop exercises a trivial code-plus-test change.","priority":"ISSUE_PRIORITY_MEDIUM","assignee":"operator"}
EOF
)"
}

print_summary() {
  local github_url=""
  if [[ "$publish_github" == true ]]; then
    github_url="https://github.com/$github_owner/$repo_name"
  fi

  cat <<EOF
rein-demo bootstrap complete
  repo_dir: $repo_dir
  state_home: $state_home
  instance: $instance
  project_id: $project_id
  seeded_issues: 5 issues (RD-1 through RD-5 on a fresh instance)
EOF
  if [[ -n "$github_url" ]]; then
    echo "  github_repo: $github_url"
  fi
}

ensure_rein_ready
ensure_local_repo
ensure_github_repo
seed_rein_project
print_summary
