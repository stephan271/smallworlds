"""Send the incident report to the admin via Stalwart's internal relay (smtplib).

Same path Alertmanager uses: unauthenticated from cluster subnets, plain port
25, no TLS; EHLO must be a valid FQDN or Stalwart rejects it.
"""
import logging
import smtplib
from email.message import EmailMessage

import config

log = logging.getLogger("mailer")


def send(subject: str, body: str) -> None:
    msg = EmailMessage()
    msg["Subject"] = subject
    msg["From"] = config.MAIL_FROM
    msg["To"] = config.ADMIN_EMAIL
    msg.set_content(body)

    with smtplib.SMTP(config.SMTP_HOST, config.SMTP_PORT, local_hostname=config.SMTP_HELO,
                      timeout=20) as smtp:
        smtp.send_message(msg)
    log.info("emailed report to %s: %s", config.ADMIN_EMAIL, subject)
