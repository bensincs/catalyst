"use client";

import {
  ArrowUpRight,
  Bot,
  Fingerprint,
  KeyRound,
  Landmark,
  Monitor,
  Moon,
  ShieldCheck,
  Sun,
  UserCog,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { useConsole } from "@/components/providers/console-provider";
import type { Role, TenantContextInfo } from "@/lib/types";
import styles from "./settings-view.module.css";

export interface SettingsIdentity {
  name: string;
  email: string;
  role: Role;
  oid: string;
  tid: string;
}

const ENTRA_URL = "https://entra.microsoft.com/";
const KEYVAULT_URL = "https://portal.azure.com/#browse/Microsoft.KeyVault%2Fvaults";
const PORTAL_URL = "https://portal.azure.com/";

export function SettingsView({
  identity,
  tenant,
}: {
  identity: SettingsIdentity;
  tenant: TenantContextInfo | null;
}) {
  const platform = identity.role === "platform";

  return (
    <div className={styles.page}>
      <PageHeader
        title="Settings"
        description={
          platform
            ? "Your identity, appearance, and where the platform is governed. Access and signing live in your directory — Cortex reflects them, it doesn't own them."
            : "Your identity, appearance, and where your tenant is governed. Admins and secrets live in your own Azure — Cortex never holds them."
        }
      />

      {/* Identity */}
      <Panel title="Identity" icon={UserCog}>
        <dl className={styles.facts}>
          <Fact label="Name" value={identity.name || "—"} />
          <Fact label="Email" value={identity.email || "—"} />
          <Fact label="Role" value={platform ? "Platform admin" : "Tenant admin"} />
          <Fact label="Object ID" value={identity.oid} mono />
          <Fact label={platform ? "Platform directory" : "Directory (tenant)"} value={identity.tid} mono />
        </dl>
        <p className={styles.note}>
          <ShieldCheck size={14} strokeWidth={2.2} aria-hidden />
          Signed in with Microsoft Entra. Cortex verifies your token on every request and never
          stores a password.
        </p>
      </Panel>

      {/* Appearance — a real, functional preference */}
      <Panel title="Appearance" icon={Monitor}>
        <div className={styles.settingRow}>
          <div className={styles.settingText}>
            <span className={styles.settingLabel}>Theme</span>
            <span className={styles.settingHelp}>
              Dark is tuned for watching reconciles land; light for daytime review.
            </span>
          </div>
          <ThemeControl />
        </div>
      </Panel>

      {/* Tenant identity (tenant only) */}
      {!platform && tenant && (
        <Panel title="Tenant identity" icon={Fingerprint}>
          <dl className={styles.facts}>
            <Fact label="Subscription" value={tenant.subscriptionId || "—"} mono />
            <Fact label="Region" value={tenant.region || "—"} />
            <Fact label="Reconciler identity" value={tenant.reconcilerIdentity || "—"} mono />
            <Fact label="Foundry project" value={tenant.foundryProject || "—"} mono />
          </dl>
        </Panel>
      )}

      {/* Governance — honest, read-only, links out to where it actually lives */}
      <Panel title="Governance" icon={ShieldCheck}>
        <ul className={styles.govList} role="list">
          {(platform ? PLATFORM_GOV : TENANT_GOV).map((g) => (
            <li key={g.title} className={styles.govItem}>
              <span className={styles.govIcon} aria-hidden>
                <g.icon size={16} strokeWidth={2} />
              </span>
              <div className={styles.govText}>
                <span className={styles.govTitle}>{g.title}</span>
                <span className={styles.govBody}>{g.body}</span>
              </div>
              <a
                className={styles.govLink}
                href={g.href}
                target="_blank"
                rel="noopener noreferrer"
              >
                {g.cta}
                <ArrowUpRight size={13} strokeWidth={2.2} aria-hidden />
              </a>
            </li>
          ))}
        </ul>
      </Panel>
    </div>
  );
}

const PLATFORM_GOV = [
  {
    icon: UserCog,
    title: "Platform administrators",
    body: "Who can author the catalog and govern the fleet is managed in your Entra directory, not in Cortex.",
    cta: "Open Entra",
    href: ENTRA_URL,
  },
  {
    icon: KeyRound,
    title: "App registration & signing",
    body: "The multi-tenant app, exposed API scope, and token signing keys live in your directory.",
    cta: "Manage app",
    href: ENTRA_URL,
  },
];

const TENANT_GOV = [
  {
    icon: UserCog,
    title: "Tenant administrators",
    body: "Invite or remove admins for this tenant in your own Entra directory; Cortex honors those roles.",
    cta: "Open Entra",
    href: ENTRA_URL,
  },
  {
    icon: KeyRound,
    title: "Connector credentials",
    body: "Secrets your agents use stay in your own Key Vault. Cortex references them; it never holds them.",
    cta: "Open Key Vault",
    href: KEYVAULT_URL,
  },
  {
    icon: Bot,
    title: "Agent identity",
    body: "Agents run under your reconciler's managed identity, in your subscription — never as Cortex.",
    cta: "Open portal",
    href: PORTAL_URL,
  },
];

function ThemeControl() {
  const { theme, toggleTheme, mounted } = useConsole();
  const options: { key: "light" | "dark"; label: string; icon: typeof Sun }[] = [
    { key: "light", label: "Light", icon: Sun },
    { key: "dark", label: "Dark", icon: Moon },
  ];
  return (
    <div className={styles.segmented} role="group" aria-label="Theme">
      {options.map((o) => {
        const active = mounted && theme === o.key;
        return (
          <button
            key={o.key}
            type="button"
            className={styles.segment}
            data-active={active || undefined}
            aria-pressed={active}
            onClick={() => {
              if (theme !== o.key) toggleTheme();
            }}
          >
            <o.icon size={14} strokeWidth={2.2} aria-hidden />
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function Panel({
  title,
  icon: Icon,
  children,
}: {
  title: string;
  icon: typeof ShieldCheck;
  children: React.ReactNode;
}) {
  return (
    <section className={styles.panel} aria-label={title}>
      <div className={styles.panelHead}>
        <span className={styles.panelIcon} aria-hidden>
          <Icon size={16} strokeWidth={2} />
        </span>
        <h2 className={styles.panelTitle}>{title}</h2>
      </div>
      {children}
    </section>
  );
}

function Fact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className={styles.fact}>
      <dt className={styles.factLabel}>{label}</dt>
      <dd className={styles.factValue + (mono ? " mono" : "")}>{value}</dd>
    </div>
  );
}
