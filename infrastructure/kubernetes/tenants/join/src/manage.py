"""Member Invitations from a shell, until the Invitation Desk exists (phase 3).

    kubectl -n join exec deploy/join -- python /app/manage.py create --locale de --label "Anna"
    kubectl -n join exec deploy/join -- python /app/manage.py list
    kubectl -n join exec deploy/join -- python /app/manage.py revoke 7 [--delete-account]

Runs inside the pod, against the same database the join page uses. The code is
printed once and is not stored; treat the output like a password until the
member has registered a passkey.
"""
import argparse
import datetime
import sys

import codes
import config
import desk
import store


def _when(epoch):
    # The pod runs in UTC; say so rather than let it pass for local time.
    if not epoch:
        return "-"
    return datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%d %H:%MZ")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="Create, list and revoke Member Invitations.")
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create", help="issue a new-member invitation")
    create.add_argument("--locale", choices=config.LOCALES, default="de")
    create.add_argument("--days", type=int, default=config.DEFAULT_DAYS)
    create.add_argument("--label", default="", help="for your own list; not the username")
    create.add_argument("--note", default="")
    commands.add_parser("list", help="every invitation and its state")
    revoke = commands.add_parser("revoke", help="revoke an invitation that is not done")
    revoke.add_argument("id", type=int)
    revoke.add_argument("--delete-account", action="store_true",
                        help="also delete the account it started, if that has no passkey")
    args = parser.parse_args(argv)

    store.init()
    if args.command == "create":
        try:
            invitation, code = store.create("new", args.locale, args.days, args.label, args.note)
        except ValueError as error:
            print(error, file=sys.stderr)
            return 2
        print(f"Invitation {invitation.id}, valid until {_when(invitation.expires_at)}")
        print(f"Code: {codes.display(code)}")
        print(f"Link: {config.JOIN_URL}/#{codes.display(code)}")
    elif args.command == "list":
        print(f"{'id':>4}  {'state':8}  {'kind':8}  {'lang':4}  {'expires (UTC)':17}  {'username':20}  label")
        for invitation in store.all_invitations():
            print(f"{invitation.id:>4}  {invitation.state():8}  {invitation.kind:8}  "
                  f"{invitation.locale:4}  {_when(invitation.expires_at):17}  "
                  f"{invitation.username or '-':20}  {invitation.label}")
    elif args.command == "revoke":
        # The Desk's own rule: an account goes only if this invitation created
        # it and it holds no passkey.
        status, answer = desk.revoke(args.id, {"deleteAccount": args.delete_account})
        if status != 200:
            print(f"Invitation {args.id} not revoked: {answer.get('error')}.", file=sys.stderr)
            return 1
        print(f"Invitation {args.id} revoked.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
