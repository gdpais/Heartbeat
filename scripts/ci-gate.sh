#!/bin/sh
# Gives CI its verdict as one check, ci-ok, the only check master requires
# (ADR 0007). GitHub counts a skipped job as passed, so a required job that was
# skipped by mistake (a broken condition, a failed dependency) would let a pull
# request merge untested. This script fails unless each job did what the
# change detection asked for.
#
# Usage: NEEDS='<toJSON(needs)>' scripts/ci-gate.sh job... job:flag...
#
#   job        must have succeeded (it always runs)
#   job:flag   runs only when the changes job printed flag=true: it must have
#              succeeded when the flag is true, and been skipped when it is false
#
# Every job in NEEDS must be named here and every name must be in NEEDS, so a
# job added to the workflow cannot be left out of the verdict. The flags are
# the outputs of the job named changes.
set -eu

: "${NEEDS:?NEEDS must hold the workflow needs context as JSON}"
[ $# -gt 0 ] || {
	echo "usage: NEEDS=... scripts/ci-gate.sh job... job:flag..." >&2
	exit 2
}

needed=$(printf '%s' "$NEEDS" | jq -r 'keys[]' | sort)
named=$(for arg in "$@"; do printf '%s\n' "${arg%%:*}"; done | sort)
if [ "$needed" != "$named" ]; then
	echo "the jobs this gate needs and the jobs it checks differ" >&2
	echo "needs: $(printf '%s' "$needed" | tr '\n' ' ')" >&2
	echo "checks: $(printf '%s' "$named" | tr '\n' ' ')" >&2
	exit 1
fi

failed=0
report="| Job | Result | Expected |
| --- | --- | --- |"
for arg in "$@"; do
	job=${arg%%:*}
	result=$(printf '%s' "$NEEDS" | jq -r --arg j "$job" '.[$j].result')
	case $arg in
	*:*)
		flag=${arg#*:}
		wanted=$(printf '%s' "$NEEDS" | jq -r --arg f "$flag" '.changes.outputs[$f] // "missing"')
		case $wanted in
		true) expected=success ;;
		false) expected=skipped ;;
		*) expected="flag $flag=$wanted" ;;
		esac
		;;
	*) expected=success ;;
	esac
	if [ "$result" = "$expected" ]; then
		verdict=ok
	else
		verdict=FAILED
		failed=1
	fi
	report="$report
| $job | $result | $expected ($verdict) |"
done

printf '%s\n' "$report"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	printf '## CI verdict\n\n%s\n' "$report" >>"$GITHUB_STEP_SUMMARY"
fi
if [ "$failed" -ne 0 ]; then
	echo "a job did not do what the change detection asked for" >&2
	exit 1
fi
