import { redirect } from "next/navigation";

// Every provider's pool now lives on /admin/accounts, a tab per provider;
// old per-provider links land on that tab.
export default async function ProviderAccountsPage({ params }: { params: Promise<{ provider: string }> }) {
  const { provider } = await params;
  redirect(`/admin/accounts?provider=${encodeURIComponent(provider)}`);
}
