#!/usr/bin/env bash
set -euo pipefail

repository="${1:?repository is required}"
upstream_source_sha="${2:?upstream source SHA is required}"
candidate_tree_sha="${3:?candidate tree SHA is required}"
publication_tree_sha="${4:?publication tree SHA is required}"
current_main_sha="${5:-}"
current_mirror_sha="${6:-}"
current_patched_sha="${7:-}"

state_changed=true

if [ -n "$current_main_sha" ] &&
  [ "$current_mirror_sha" = "$current_main_sha" ] &&
  [ -n "$current_patched_sha" ] &&
  git -C "$repository" cat-file -e "${current_main_sha}^{commit}" &&
  git -C "$repository" cat-file -e "${current_patched_sha}^{commit}"; then
  read -r -a main_line <<< "$(
    git -C "$repository" rev-list --parents -n 1 "$current_main_sha"
  )"

  if [ "${#main_line[@]}" -eq 3 ]; then
    workflow_install_sha="${main_line[2]}"
    read -r -a workflow_line <<< "$(
      git -C "$repository" rev-list --parents -n 1 "$workflow_install_sha"
    )"

    if [ "${#workflow_line[@]}" -eq 2 ] &&
      [ "${workflow_line[1]}" = "$upstream_source_sha" ] &&
      [ "$(git -C "$repository" rev-parse "${current_main_sha}^{tree}")" = "$candidate_tree_sha" ] &&
      [ "$(git -C "$repository" rev-parse "${workflow_install_sha}^{tree}")" = "$candidate_tree_sha" ] &&
      [ "$(git -C "$repository" rev-parse "${current_patched_sha}^{tree}")" = "$publication_tree_sha" ]; then
      state_changed=false
    fi
  fi
fi

printf '%s\n' "$state_changed"
