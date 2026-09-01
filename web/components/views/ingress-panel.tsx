"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Globe, Save, ShieldCheck, Copy, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { setAppsDomain, setOIDCConfig } from "@/lib/actions";
import type { TenantIngress } from "@/lib/types";
import panel from "./entitlements-panel.module.css";
import styles from "./tenant-members-panel.module.css";

const DNS_TONE = { verified: "success", pending: "info", failed: "danger" } as const;
const DNS_LABEL = { verified: "Delegated", pending: "Awaiting delegation", failed: "Failed" } as const;

/** How a tenant publishes apps: the domain they delegate to us, and the OIDC
 *  application that guards what we publish there.
 *
 *  A delegated domain is the ONLY way apps become reachable — there is no
 *  platform-supplied fallback hostname. Everything downstream (the wildcard
 *  record, the wildcard certificate, every app URL) follows from it. */
export function IngressPanel({
  slug,
  name,
  ingress,
}: {
  slug: string;
  name: string;
  ingress: TenantIngress;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();

  const [domain, setDomain] = useState(ingress.appsDomain ?? "");
  const [issuer, setIssuer] = useState(ingress.oidcIssuer ?? "");
  const [clientId, setClientId] = useState(ingress.oidcClientId ?? "");
  const [clientSecret, setClientSecret] = useState("");
  const [copied, setCopied] = useState(false);

  const state = (ingress.dnsState || "") as keyof typeof DNS_LABEL;
  const nameservers = ingress.dnsNameservers ?? [];

  const saveDomain = () =>
    start(async () => {
      const res = await setAppsDomain(slug, domain.trim());
      if (res.ok) {
        toast({
          title: domain.trim() ? "Domain saved" : "Domain cleared",
          description: domain.trim()
            ? "Creating the DNS zone — the nameservers to delegate to will appear here shortly."
            : "Apps are no longer published.",
          tone: "success",
        });
        router.refresh();
      } else {
        toast({ title: "Couldn't save the domain", description: res.error, tone: "danger" });
      }
    });

  const saveOIDC = () =>
    start(async () => {
      const res = await setOIDCConfig(slug, {
        oidcIssuer: issuer.trim(),
        oidcClientId: clientId.trim(),
        oidcClientSecret: clientSecret,
      });
      if (res.ok) {
        setClientSecret("");
        toast({ title: "OIDC saved", description: "Apps set to require a login will use it.", tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't save OIDC", description: res.error, tone: "danger" });
      }
    });

  const copyNS = async () => {
    try {
      await navigator.clipboard.writeText(nameservers.join("\n"));
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      toast({ title: "Couldn't copy", description: "Copy the nameservers manually.", tone: "danger" });
    }
  };

  return (
    <section className={panel.panel} aria-label="Ingress">
      <div className={panel.head}>
        <div className={panel.headText}>
          <h2 className={panel.title}>
            Domain{" "}
            {ingress.appsDomain ? (
              <StatusBadge
                tone={DNS_TONE[state] ?? "neutral"}
                label={DNS_LABEL[state] ?? "Not configured"}
                variant="soft"
                pulse={state === "pending"}
              />
            ) : null}
          </h2>
          <p className={panel.sub}>
            {name}&apos;s apps are published at <code>&lt;app&gt;.&lt;domain&gt;</code>. The zone is
            created in this tenant&apos;s own subscription and its reconciler manages the records
            and certificate — nothing leaves the tenant. Apps are not reachable until this is set.
          </p>
        </div>
      </div>

      <div style={{ marginTop: 14, display: "flex", flexDirection: "column", gap: 12 }}>
        <div style={{ display: "flex", gap: 8, alignItems: "flex-end" }}>
          <div style={{ flex: 1 }}>
            <Field label="Apps domain" value={domain} onChange={setDomain} placeholder="apps.contoso.com" />
          </div>
          <Button variant="ghost" icon={Save} loading={pending} onClick={saveDomain}>
            Save
          </Button>
        </div>

        {ingress.dnsDetail ? <p className={panel.sub} style={{ margin: 0 }}>{ingress.dnsDetail}</p> : null}

        {nameservers.length > 0 && state !== "verified" ? (
          <div
            style={{
              padding: "12px 14px",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-md)",
              background: "var(--surface)",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}>
              <Globe size={15} aria-hidden style={{ opacity: 0.7 }} />
              <strong style={{ fontSize: "var(--text-body-sm)" }}>
                Set these nameservers for {ingress.appsDomain}
              </strong>
              <span style={{ flex: 1 }} />
              <Button variant="ghost" icon={copied ? Check : Copy} onClick={copyNS}>
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
            <ul style={{ margin: 0, paddingLeft: 18, fontSize: 12, fontFamily: "var(--font-mono, monospace)" }}>
              {nameservers.map((ns) => (
                <li key={ns}>{ns}</li>
              ))}
            </ul>
            <p className={panel.sub} style={{ margin: "8px 0 0" }}>
              Delegation is checked against public DNS, so this clears itself once the records are
              live. Certificates are issued automatically after that.
            </p>
          </div>
        ) : null}

        {state === "verified" ? (
          <p style={{ display: "flex", alignItems: "center", gap: 8, margin: 0, fontSize: 12, color: "var(--text-secondary)" }}>
            <ShieldCheck size={14} aria-hidden />
            <span>
              {ingress.tlsReady
                ? `Wildcard certificate active${ingress.tlsExpiresAt ? ` until ${new Date(ingress.tlsExpiresAt).toLocaleDateString()}` : ""} — renewed automatically.`
                : ingress.tlsDetail || "Requesting a wildcard certificate…"}
            </span>
          </p>
        ) : null}

        <hr style={{ border: 0, borderTop: "1px solid var(--border)", margin: "4px 0" }} />

        <div>
          <h3 style={{ fontSize: "var(--text-body)", fontWeight: 600, margin: "0 0 4px" }}>
            Sign-in{" "}
            {ingress.oidcSecretSet ? (
              <StatusBadge tone="success" label="Configured" variant="soft" />
            ) : (
              <StatusBadge tone="neutral" label="Not set" variant="soft" />
            )}
          </h3>
          <p className={panel.sub} style={{ marginTop: 0 }}>
            The customer&apos;s OIDC application. Apps marked &quot;requires sign-in&quot; are put
            behind it. Each app names its own scope, and each app&apos;s callback URL must be
            registered as a redirect URI on this app registration.
          </p>
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit,minmax(220px,1fr))", gap: 8 }}>
          <Field
            label="Issuer"
            value={issuer}
            onChange={setIssuer}
            placeholder="https://login.microsoftonline.com/<tenant-id>/v2.0"
          />
          <Field label="Client ID" value={clientId} onChange={setClientId} placeholder="00000000-0000-0000-0000-000000000000" />
          <Field
            label={ingress.oidcSecretSet ? "Client secret (stored — leave blank to keep)" : "Client secret"}
            value={clientSecret}
            onChange={setClientSecret}
            placeholder={ingress.oidcSecretSet ? "••••••••" : "paste the secret value"}
            type="password"
          />
        </div>

        <div>
          <Button variant="ghost" icon={Save} loading={pending} onClick={saveOIDC}>
            Save sign-in
          </Button>
        </div>
      </div>
    </section>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>{label}</span>
      <input
        className={styles.input}
        style={{ paddingLeft: 12 }}
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
      />
    </label>
  );
}
