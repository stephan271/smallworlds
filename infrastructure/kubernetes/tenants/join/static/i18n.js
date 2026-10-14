/* Every string the join and done pages show, in German and English (design §8).
 * Keys double as the error keys the server answers with, so a new server-side
 * reason needs an entry here in both languages. */
window.JOIN_TEXT = {
  de: {
    "page.title": "Einladung",
    "lang.label": "Sprache",
    "noscript": "Diese Seite braucht JavaScript. Bitte schalte es ein oder öffne den Link in einem anderen Browser.",

    "enter.title": "Deine Einladung",
    "enter.text": "Gib den Code von deiner Einladung ein. Er hat zehn Zeichen, zum Beispiel K7QFM-2PDX9.",
    "enter.label": "Einladungscode",
    "enter.submit": "Weiter",
    "loading": "Einen Moment …",

    "welcome.title": "Willkommen bei {host}!",
    "welcome.intro": "Du bist eingeladen. Das Einrichten dauert etwa drei Minuten:",
    "welcome.step1": "Du wählst deinen Benutzernamen.",
    "welcome.step2": "Dein Gerät speichert einen Passkey. Damit meldest du dich an – mit Fingerabdruck, Gesicht oder PIN, ganz ohne Passwort.",
    "welcome.step3": "Du bekommst Wiederherstellungs-Codes. Halte Papier und Stift bereit: Mit diesen Codes kommst du wieder hinein, falls du dein Gerät verlierst.",
    "welcome.start": "Los geht’s",
    "device.no_platform": "Dieses Gerät hat anscheinend keinen eingebauten Passkey. Das ist kein Problem: Du kannst gleich dein Handy (per QR-Code) oder einen Sicherheitsschlüssel verwenden.",
    "device.unsupported": "Dieser Browser kann keine Passkeys anlegen. Öffne den Link am besten in einem aktuellen Safari, Chrome, Edge oder Firefox.",

    "form.title": "Über dich",
    "form.username": "Benutzername",
    "form.username_hint": "3 bis 32 Zeichen: Kleinbuchstaben und Ziffern, dazwischen auch . _ oder -. Damit meldest du dich an; ändern lässt er sich später nicht.",
    "form.firstName": "Vorname",
    "form.lastName": "Nachname",
    "form.email": "E-Mail-Adresse",
    "form.email_hint": "Nur damit wir dich erreichen können. Es wird keine Mail verschickt.",
    "form.submit": "Weiter zum Passkey",
    "form.has_problems": "Bitte prüfe die markierten Felder.",

    "username.checking": "Wird geprüft …",
    "username.available": "„{username}“ ist frei.",
    "username_taken": "Dieser Name ist schon vergeben.",
    "username_length": "Der Name muss 3 bis 32 Zeichen lang sein.",
    "username_characters": "Nur Kleinbuchstaben und Ziffern, dazwischen einzeln . _ oder -.",
    "username_reserved": "Dieser Name ist reserviert. Bitte wähle einen anderen.",
    "required": "Bitte ausfüllen.",
    "name_characters": "Dieser Name enthält Zeichen, die nicht erlaubt sind.",
    "email_format": "Das sieht nicht nach einer E-Mail-Adresse aus.",
    "email_taken": "Diese Adresse gehört schon zu einem Konto.",

    "resume.title": "Willkommen zurück, {username}!",
    "resume.text": "Dein Konto ist angelegt, aber es hat noch keinen Passkey. Mach dort weiter, wo du aufgehört hast.",
    "resume.submit": "Weiter zum Passkey",
    "recovery.title": "Hallo {username}",
    "recovery.text": "Hier richtest du einen neuen Passkey ein. Du bekommst dabei auch neue Wiederherstellungs-Codes – halte Papier und Stift bereit.",
    "recovery.submit": "Neuen Passkey einrichten",
    "redirecting": "Du wirst weitergeleitet …",

    "problem.title": "Das hat nicht geklappt",
    "problem.ask": "Wende dich an die Person, die dich eingeladen hat. Sie kann dir eine neue Einladung geben.",
    "problem.retry": "Nochmal versuchen",
    "problem.other_code": "Anderen Code eingeben",
    "unknown": "Diesen Einladungscode gibt es nicht. Prüfe, ob du ihn richtig abgeschrieben hast.",
    "expired": "Diese Einladung ist abgelaufen.",
    "revoked": "Diese Einladung wurde zurückgezogen.",
    "used": "Diese Einladung wurde schon verwendet. Melde dich einfach mit deinem Passkey an.",
    "account_gone": "Das Konto zu dieser Einladung gibt es nicht mehr.",
    "too_many": "Zu viele Versuche. Bitte warte ein paar Minuten.",
    "unavailable": "Der Dienst ist gerade nicht erreichbar. Bitte versuche es gleich noch einmal.",
    "no_choice": "Der Benutzername für diese Einladung steht schon fest.",
    "conflict": "Diese Einladung wird gerade schon verwendet.",

    "done.title": "Geschafft!",
    "done.text": "Dein Konto ist fertig. Ab jetzt meldest du dich überall mit deinem Passkey an.",
    "done.codes": "Bewahre deine Wiederherstellungs-Codes an einem sicheren Ort auf – nicht nur auf diesem Gerät.",
    "done.dashboard": "Zu deinem Dashboard",
  },
  en: {
    "page.title": "Invitation",
    "lang.label": "Language",
    "noscript": "This page needs JavaScript. Please turn it on, or open the link in another browser.",

    "enter.title": "Your invitation",
    "enter.text": "Enter the code from your invitation. It has ten characters, for example K7QFM-2PDX9.",
    "enter.label": "Invitation code",
    "enter.submit": "Continue",
    "loading": "One moment …",

    "welcome.title": "Welcome to {host}!",
    "welcome.intro": "You have been invited. Setting up takes about three minutes:",
    "welcome.step1": "You choose your username.",
    "welcome.step2": "Your device saves a passkey. That is how you sign in – with your fingerprint, face or PIN, and no password at all.",
    "welcome.step3": "You get recovery codes. Have pen and paper ready: these codes get you back in if you lose your device.",
    "welcome.start": "Let’s start",
    "device.no_platform": "This device does not seem to have a built-in passkey. That is fine: you can use your phone (by QR code) or a security key in a moment.",
    "device.unsupported": "This browser cannot create passkeys. Please open the link in an up-to-date Safari, Chrome, Edge or Firefox.",

    "form.title": "About you",
    "form.username": "Username",
    "form.username_hint": "3 to 32 characters: lowercase letters and digits, with . _ or - in between. You sign in with it, and it cannot be changed later.",
    "form.firstName": "First name",
    "form.lastName": "Last name",
    "form.email": "Email address",
    "form.email_hint": "Only so that we can reach you. No mail is sent.",
    "form.submit": "Continue to the passkey",
    "form.has_problems": "Please check the marked fields.",

    "username.checking": "Checking …",
    "username.available": "“{username}” is available.",
    "username_taken": "This name is taken.",
    "username_length": "The name must be 3 to 32 characters long.",
    "username_characters": "Only lowercase letters and digits, with a single . _ or - in between.",
    "username_reserved": "This name is reserved. Please choose another one.",
    "required": "Please fill this in.",
    "name_characters": "This name contains characters that are not allowed.",
    "email_format": "That does not look like an email address.",
    "email_taken": "This address already belongs to an account.",

    "resume.title": "Welcome back, {username}!",
    "resume.text": "Your account exists, but it has no passkey yet. Carry on where you left off.",
    "resume.submit": "Continue to the passkey",
    "recovery.title": "Hello {username}",
    "recovery.text": "Here you set up a new passkey. You will also get new recovery codes – have pen and paper ready.",
    "recovery.submit": "Set up a new passkey",
    "redirecting": "Taking you there …",

    "problem.title": "That did not work",
    "problem.ask": "Please contact the person who invited you. They can give you a new invitation.",
    "problem.retry": "Try again",
    "problem.other_code": "Enter another code",
    "unknown": "This invitation code does not exist. Check that you copied it correctly.",
    "expired": "This invitation has expired.",
    "revoked": "This invitation has been withdrawn.",
    "used": "This invitation has been used already. Just sign in with your passkey.",
    "account_gone": "The account for this invitation no longer exists.",
    "too_many": "Too many attempts. Please wait a few minutes.",
    "unavailable": "The service cannot be reached right now. Please try again in a moment.",
    "no_choice": "The username for this invitation is already fixed.",
    "conflict": "This invitation is being used right now.",

    "done.title": "All done!",
    "done.text": "Your account is ready. From now on you sign in everywhere with your passkey.",
    "done.codes": "Keep your recovery codes somewhere safe – not only on this device.",
    "done.dashboard": "To your dashboard",
  },
};

/* Shared by both pages: language choice and applying it to [data-t] elements.
 * The member's own switch wins over the invitation's language, which wins over
 * the browser's (design §8). sessionStorage can be missing or throw. */
window.joinI18n = (() => {
  const text = window.JOIN_TEXT;
  const vars = { host: location.hostname.replace(/^join\./, "") };
  let chosen = null;
  try {
    const saved = sessionStorage.getItem("join-lang");
    if (saved in text) chosen = saved;
  } catch (_) { /* private mode */ }
  const fromBrowser = () =>
    (navigator.languages || [navigator.language || "en"])
      .map((l) => String(l).slice(0, 2).toLowerCase())
      .find((l) => l in text) || "en";
  let lang = chosen || fromBrowser();

  // `extra` fills placeholders for one call; an element can carry its own in
  // data-t-vars (JSON), so a language switch re-renders it with them.
  const t = (key, extra = {}) =>
    (text[lang][key] ?? text.en[key] ?? key)
      .replace(/\{(\w+)\}/g, (_, name) => extra[name] ?? vars[name] ?? "");

  const apply = () => {
    document.documentElement.lang = lang;
    document.title = `${t("page.title")} · ${vars.host}`;
    document.querySelectorAll("[data-t]").forEach((el) => {
      el.textContent = t(el.dataset.t, el.dataset.tVars ? JSON.parse(el.dataset.tVars) : {});
    });
    document.querySelectorAll("[data-t-placeholder]").forEach((el) => {
      el.placeholder = t(el.dataset.tPlaceholder);
    });
    document.querySelectorAll("[data-lang]").forEach((button) => {
      button.setAttribute("aria-pressed", String(button.dataset.lang === lang));
    });
  };

  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-lang]");
    if (!button) return;
    lang = button.dataset.lang;
    chosen = lang;
    try { sessionStorage.setItem("join-lang", lang); } catch (_) { /* ignore */ }
    apply();
  });

  return {
    t, apply, vars,
    get lang() { return lang; },
    get chosen() { return chosen !== null; },
    // The invitation's language, unless the member has picked one themselves.
    suggest(locale) { if (!chosen && locale in text) { lang = locale; apply(); } },
    force(locale) { if (locale in text) { lang = locale; apply(); } },
  };
})();
