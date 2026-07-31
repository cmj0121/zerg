#!/usr/bin/env bash
#
# ci-fetch-testdata — make the test-data submodule available, and be honest when it is not.
#
# `test-data` is a PRIVATE repository declared over SSH, which a CI checkout has no key
# for. The workflow used to paper over that with `continue-on-error: true` plus a gate on
# `steps.<id>.outcome`: the failure was reported as a SUCCESSFUL step while `outcome` kept
# `failure`, so `make corpus` and `make sanitize-conc` were SKIPPED on every run, inside
# jobs that went green. Nobody reading the check list could tell.
#
# There are two legitimate worlds and this script distinguishes them:
#
#   TESTDATA_TOKEN set    — the corpus is REQUIRED. A fetch that breaks fails the job,
#                           because with credentials in hand a failure is a real problem.
#   TESTDATA_TOKEN unset  — a fork, or this repo before the secret exists. The gate cannot
#                           run, and the job says so with a warning annotation naming it.
#                           A green check then means "everything that could run, ran".
#
# Usage: ci-fetch-testdata.sh <gate-name>      # e.g. corpus, sanitize-conc
# Writes `available=true|false` to $GITHUB_OUTPUT for the step that follows to gate on.

set -u

gate=${1:?usage: ci-fetch-testdata.sh <gate-name>}
out=${GITHUB_OUTPUT:-/dev/null}

if [ -n "${TESTDATA_TOKEN:-}" ]; then
	# x-access-token is the username GitHub expects for a token over HTTPS; the token
	# itself never reaches the log, because git reads the URL from the config file.
	git config --global url."https://x-access-token:${TESTDATA_TOKEN}@github.com/".insteadOf "git@github.com:"
	# The status is CHECKED. Running the command and then announcing success regardless
	# is the exact shape of the defect this script exists to remove.
	if ! git submodule update --init --recursive; then
		echo "available=false" >>"$out"
		echo "::error title=${gate} cannot run::TESTDATA_TOKEN is set but the test-data fetch failed — the ${gate} gate has credentials and still could not get its cases"
		exit 1
	fi
	echo "available=true" >>"$out"
	exit 0
fi

git config --global url."https://github.com/".insteadOf "git@github.com:"
if git submodule update --init --recursive; then
	echo "available=true" >>"$out"
	exit 0
fi

echo "available=false" >>"$out"
echo "::warning title=${gate} did not run::test-data is unreachable without the TESTDATA_TOKEN secret, so the ${gate} gate was skipped — this job being green does not mean it passed"
exit 0
