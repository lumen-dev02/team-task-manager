#!/usr/bin/env bash
set -euo pipefail

# Requires curl, jq and openssl. Uses fresh accounts so it can be run repeatedly.
BASE_URL="${BASE_URL:-http://localhost:8080}"
BASE_URL="${BASE_URL%/}"
suffix="$(openssl rand -hex 6)"
owner_email="owner-${suffix}@example.com"
member_email="member-${suffix}@example.com"
password="$(openssl rand -hex 16)"

api() {
  local method="$1" path="$2" token="$3" body="${4:-}"
  local -a args=(-sS --fail-with-body -X "$method" "${BASE_URL}/api/v1${path}")
  if [[ -n "$token" ]]; then args+=(-H "Authorization: Bearer $token"); fi
  if [[ -n "$body" ]]; then args+=(-H 'Content-Type: application/json' -d "$body"); fi
  curl "${args[@]}"
}

api POST /auth/register '' "$(jq -n --arg email "$owner_email" --arg p "$password" '{email:$email,name:"Owner",password:$p}')" >/dev/null
member_id="$(api POST /auth/register '' "$(jq -n --arg email "$member_email" --arg p "$password" '{email:$email,name:"Member",password:$p}')" | jq -er '.id')"
owner_token="$(api POST /auth/login '' "$(jq -n --arg email "$owner_email" --arg p "$password" '{email:$email,password:$p}')" | jq -er '.access_token')"
member_token="$(api POST /auth/login '' "$(jq -n --arg email "$member_email" --arg p "$password" '{email:$email,password:$p}')" | jq -er '.access_token')"

project_id="$(api POST /projects "$owner_token" '{"name":"Course project","description":"API demo"}' | jq -er '.id')"
project="/projects/${project_id}"
api POST "${project}/members" "$owner_token" "$(jq -n --arg email "$member_email" '{email:$email}')"
task="$(api POST "${project}/tasks" "$member_token" "$(jq -n --argjson id "$member_id" '{title:"Write API tests",assignee_id:$id}')")"
task_id="$(jq -er '.id' <<<"$task")"
version="$(jq -er '.version' <<<"$task")"

echo 'Update task:'
api PUT "${project}/tasks/${task_id}" "$member_token" "$(jq -n --argjson id "$member_id" --argjson v "$version" '{title:"Write API tests",description:"Check project permissions",status:"in_progress",assignee_id:$id,version:$v}')" | jq .
echo 'Status history:'
api GET "${project}/tasks/${task_id}/history" "$owner_token" | jq .
api DELETE "${project}/members/${member_id}" "$owner_token"
echo 'Task after member removal (assignee_id should be null):'
api GET "${project}/tasks/${task_id}" "$owner_token" | jq .
echo "Demo project ${project_id} is kept in the database. No real credentials were used."
