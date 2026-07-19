export type Locale = 'en' | 'de';

const messages = {
  en: {
    product: 'SmallWorlds Operator Console',
    subtitle: 'Set up and understand your cluster from one private place.',
    profiles: 'Cluster Profiles',
    createAnother: 'Create another profile',
    createTitle: 'Create Cluster Profile',
    profileName: 'Profile name',
    language: 'Language',
    deploymentMode: 'Deployment mode',
    localLan: 'Local LAN-only',
    localPublic: 'Local internet-exposed',
    hetzner: 'Hetzner-hosted',
    createProfile: 'Create profile',
    editProfile: 'Edit profile',
    saveProfile: 'Save profile',
    cancel: 'Cancel',
    next: 'Next recommended action',
    task: 'Verify launcher',
    taskDescription: 'Run a harmless workflow to verify planning, persistence, progress, and observed evidence.',
    inspectPlan: 'Inspect and plan',
    planTitle: 'Change Plan',
    effect: 'Record verified launcher evidence',
    noRisk: 'No cost, downtime, exposure, data, or lockout risk.',
    digest: 'Plan digest',
    approve: 'Approve and run',
    activity: 'Activity',
    ready: 'Ready',
    running: 'Running',
    verified: 'Verified',
    cancelled: 'Cancelled',
    failed: 'Failed',
    retry: 'Try again',
    loading: 'Opening secure launcher session…'
  },
  de: {
    product: 'SmallWorlds Operator Console',
    subtitle: 'Richten Sie Ihren Cluster an einem privaten Ort ein und behalten Sie ihn im Blick.',
    profiles: 'Clusterprofile',
    createAnother: 'Weiteres Profil erstellen',
    createTitle: 'Clusterprofil erstellen',
    profileName: 'Profilname',
    language: 'Sprache',
    deploymentMode: 'Bereitstellungsmodus',
    localLan: 'Lokal, nur LAN',
    localPublic: 'Lokal, internet-erreichbar',
    hetzner: 'Bei Hetzner gehostet',
    createProfile: 'Profil erstellen',
    editProfile: 'Profil bearbeiten',
    saveProfile: 'Profil speichern',
    cancel: 'Abbrechen',
    next: 'Nächste empfohlene Aktion',
    task: 'Launcher überprüfen',
    taskDescription: 'Ein harmloser Ablauf überprüft Planung, Speicherung, Fortschritt und beobachtete Nachweise.',
    inspectPlan: 'Prüfen und planen',
    planTitle: 'Änderungsplan',
    effect: 'Geprüften Launcher-Nachweis festhalten',
    noRisk: 'Keine Kosten-, Ausfall-, Freigabe-, Daten- oder Aussperrungsrisiken.',
    digest: 'Plan-Prüfsumme',
    approve: 'Genehmigen und ausführen',
    activity: 'Aktivität',
    ready: 'Bereit',
    running: 'Wird ausgeführt',
    verified: 'Verifiziert',
    cancelled: 'Abgebrochen',
    failed: 'Fehlgeschlagen',
    retry: 'Erneut versuchen',
    loading: 'Sichere Launcher-Sitzung wird geöffnet…'
  }
} as const;

export type MessageKey = keyof (typeof messages)['en'];

export function translate(locale: Locale, key: MessageKey): string {
  return messages[locale][key];
}
