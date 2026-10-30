import { Network } from "lucide-react";
import { EmptyState, PageHeader } from "@/components/qanat/page";

export const metadata = { title: "Proxies" };

export default function Page() {
  return (
    <>
      <PageHeader title="Proxies" icon={Network} />
      <EmptyState title="No proxies">Outbound proxies and proxy groups arrive in P1.</EmptyState>
    </>
  );
}
