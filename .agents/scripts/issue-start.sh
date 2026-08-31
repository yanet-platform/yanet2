#!/usr/bin/env bash
# Start work on an issue: assign the caller and set the board Status to
# In Progress on the managed planning boards.
#
# Idempotent. All node IDs are discovered live, never hardcoded.
set -euo pipefail

usage() {
	echo "usage: ${0##*/} <owner>/<repo> <issue-number>" >&2
	exit 64
}

[[ $# -eq 2 ]] || usage
repo=$1
number=$2
[[ $repo =~ ^[^/]+/[^/]+$ ]] || usage
[[ $number =~ ^[0-9]+$ ]] || usage
owner=${repo%%/*}
name=${repo#*/}

# The planning boards issue-create manages. Projects outside this set are
# never mutated.
managed_boards=" 7 8 9 10 11 "

viewer=$(gh api graphql -f query='{viewer{login}}' -q .data.viewer.login)

issue_json=$(gh api graphql \
	-f query='query($owner:String!,$name:String!,$number:Int!){
	  repository(owner:$owner,name:$name){
	    issue(number:$number){
	      state
	      assignees(first:10){nodes{login}}
	      projectItems(first:20){nodes{
	        id
	        fieldValueByName(name:"Status"){
	          ... on ProjectV2ItemFieldSingleSelectValue { name }
	        }
	        project{
	          id number title
	          field(name:"Status"){
	            ... on ProjectV2SingleSelectField { id options { id name } }
	          }
	        }
	      }}
	    }
	  }
	}' -f owner="$owner" -f name="$name" -F number="$number")

state=$(jq -r .data.repository.issue.state <<<"$issue_json")
if [[ $state != OPEN ]]; then
	echo "error: issue #$number in $repo is $state" >&2
	exit 1
fi

mapfile -t assignees < <(jq -r '.data.repository.issue.assignees.nodes[].login' <<<"$issue_json")
for login in "${assignees[@]}"; do
	if [[ $login != "$viewer" ]]; then
		echo "error: issue #$number is assigned to $login, not $viewer" >&2
		exit 1
	fi
done

if [[ ${#assignees[@]} -eq 0 ]]; then
	gh issue edit "$number" --repo "$repo" --add-assignee "@me" >/dev/null
	echo "issue #$number: assigned $viewer"
else
	echo "issue #$number: already assigned to $viewer"
fi

on_board=0
while IFS=$'\t' read -r item_id project_id project_number title field_id option_id current; do
	[[ $managed_boards == *" $project_number "* ]] || continue
	on_board=1
	if [[ $current == "In Progress" ]]; then
		echo "board #$project_number ($title): already In Progress"
		continue
	fi
	if [[ -z $field_id || -z $option_id ]]; then
		echo "warning: board #$project_number ($title) has no Status field with an In Progress option" >&2
		continue
	fi
	gh api graphql \
		-f query='mutation($projectId:ID!,$itemId:ID!,$fieldId:ID!,$optionId:String!){
		  updateProjectV2ItemFieldValue(input:{
		    projectId:$projectId,itemId:$itemId,fieldId:$fieldId,
		    value:{singleSelectOptionId:$optionId}
		  }){clientMutationId}
		}' -f projectId="$project_id" -f itemId="$item_id" \
		-f fieldId="$field_id" -f optionId="$option_id" >/dev/null
	echo "board #$project_number ($title): ${current:-unset} -> In Progress"
done < <(jq -r '.data.repository.issue.projectItems.nodes[] |
	[.id, .project.id, (.project.number|tostring), .project.title,
	 (.project.field.id // ""),
	 ((.project.field.options // []) | map(select(.name=="In Progress")) | .[0].id // ""),
	 (.fieldValueByName.name // "")] | @tsv' <<<"$issue_json")

if [[ $on_board -eq 0 ]]; then
	echo "warning: issue #$number is on no managed board; status unchanged" >&2
fi
