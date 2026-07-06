-- Representative fleet seed. Idempotent (ON CONFLICT DO NOTHING).
-- These are seed records living in the DB — not hardcoded in the console.
-- last_heartbeat uses relative intervals so "last seen" is a real, aging value.

INSERT INTO tenants
  (id, name, tenant_id, region, plan, enrollment, version,
   agent_count, reconciling_count, monthly_calls, drift, last_heartbeat,
   subscription_id, reconciler_identity, foundry_project, reconciler_version, installed_at)
VALUES
  ('g42-cloud','G42 Cloud','4c9b1d70-3a68-4e15-9f2b-7d0a6c3e8841','UAE Central','sovereign','bound','1.6.2',
   27,0,5233910,0, now() - interval '4 seconds',
   'g42-cortex-prod-01','id-cortex-recon-g42','g42-foundry/agents-prod','1.6.2','2026-04-02'),
  ('aiq','AIQ','8b1a9f37-4e2c-4a68-b0d5-7c3e1a5f9042','UAE Central','enterprise','bound','1.6.2',
   13,0,1566300,0, now() - interval '6 seconds',
   'aiq-cortex-prod-01','id-cortex-recon-aiq','aiq-foundry/agents-prod','1.6.2','2026-04-18'),
  ('adnoc','ADNOC','b41c9e02-7a55-4f19-8d3c-6e2f0b8a4410','UAE Central','sovereign','bound','1.6.2',
   22,3,3902150,3, now() - interval '8 seconds',
   'adnoc-cortex-prod-01','id-cortex-recon-adnoc','adnoc-foundry/agents-prod','1.6.2','2026-03-21'),
  ('mubadala-health','Mubadala Health','7d3a1f88-24e1-4c0a-9b2e-1a7c5e9f0021','UAE North','sovereign','bound','1.6.2',
   14,0,1284400,0, now() - interval '12 seconds',
   'a2f4-cortex-prod-01','id-cortex-recon-mbh','mbh-foundry/agents-prod','1.6.2','2026-05-14'),
  ('emirates-nbd','Emirates NBD','9a0e4d12-6c78-49b3-8f21-3b5d9c0a7e64','UAE North','enterprise','bound','1.6.2',
   11,0,1004260,0, now() - interval '15 seconds',
   'enbd-cortex-prod-01','id-cortex-recon-enbd','enbd-foundry/agents-prod','1.6.2','2026-05-02'),
  ('khazna','Khazna Data Centers','6a4d0b21-8c73-4f52-a19e-2f7b3c5d0088','UAE Central','enterprise','bound','1.6.2',
   5,0,204540,0, now() - interval '19 seconds',
   'khazna-cortex-prod-01','id-cortex-recon-khz','khz-foundry/agents-prod','1.6.2','2026-05-20'),
  ('santander-uk','Santander UK','1f9d7c33-b0a4-42e8-95af-0c71e3d28806','West Europe','enterprise','bound','1.6.1',
   9,0,842900,0, now() - interval '21 seconds',
   'san-cortex-prod-01','id-cortex-recon-san','san-foundry/agents-prod','1.6.1','2026-04-11'),
  ('masdar','Masdar','2e8c5a44-7f11-4b90-9d3e-0a4c6b1f8827','UAE North','enterprise','bound','1.6.1',
   6,0,288120,0, now() - interval '33 seconds',
   'masdar-cortex-prod-01','id-cortex-recon-msd','msd-foundry/agents-prod','1.6.1','2026-04-28'),
  ('aldar','Aldar Properties','3d6f2c88-1b09-4e3a-9c47-5a8d0e2b6619','UAE North','team','pending','1.6.2',
   0,2,0,2, now() - interval '44 seconds',
   '','id-cortex-recon-ald','','1.6.2',''),
  ('presight','Presight AI','c73f1b90-2d4a-4e57-b8c9-1e6a2f0d3355','UAE Central','enterprise','bound','1.6.2',
   7,1,356780,1, now() - interval '100 seconds',
   'presight-cortex-prod-01','id-cortex-recon-prs','prs-foundry/agents-prod','1.6.2','2026-05-09'),
  ('moi-sovereign','Ministry of Interior','5c2b8a71-9e3f-4d6b-a1c0-88f24e7b1d55','UAE Gov (Sovereign)','sovereign','bound','1.5.4',
   18,0,2115000,2, now() - interval '242 seconds',
   'moi-cortex-prod-01','id-cortex-recon-moi','moi-foundry/agents-prod','1.5.4','2026-02-15'),
  ('etihad','Etihad Airways','0f3e7a56-9d21-4c84-b6a0-4e1c8b2f5573','UAE North','enterprise','suspended','1.5.4',
   8,0,61020,0, now() - interval '19 hours',
   'etihad-cortex-prod-01','id-cortex-recon-eth','eth-foundry/agents-prod','1.5.4','2026-01-30')
ON CONFLICT (id) DO NOTHING;

-- Enabled agents for Mubadala Health (used by the tenant-overview drill-in).
INSERT INTO agents (id, tenant_slug, agent_id, name, version, channel, model, health, publish_to, calls_30d, note, sort_order)
VALUES
  ('mubadala-health:clinical-coding','mubadala-health','clinical-coding','Clinical Coding Assistant','2.3.1','stable','gpt-4o','healthy','{api,teams}',412800,'',1),
  ('mubadala-health:prior-auth','mubadala-health','prior-auth','Prior Authorization Reviewer','1.8.0','stable','gpt-4o','healthy','{api}',268120,'',2),
  ('mubadala-health:patient-intake','mubadala-health','patient-intake','Patient Intake Triage','1.2.0','beta','gpt-4o-mini','reconciling','{api,teams}',96540,'Applying config change',3),
  ('mubadala-health:policy-qa','mubadala-health','policy-qa','Policy & Compliance Q&A','3.0.2','stable','jais-30b','healthy','{api,m365}',154300,'',4),
  ('mubadala-health:radiology-report','mubadala-health','radiology-report','Radiology Report Drafting','0.9.4','beta','gpt-4o','drift','{api}',38900,'Grounding index out of date',5)
ON CONFLICT (id) DO NOTHING;
