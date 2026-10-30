import { BarChart3 } from "lucide-react";
import { EmptyState, PageHeader } from "@/components/qanat/page";

export const metadata = { title: "Usage" };

export default function Page() {
  return (
    <>
      <PageHeader title="Usage" icon={BarChart3} />
      <EmptyState title="No usage yet">Charts of your spend and tokens by model, key and token type appear once the gateway records requests.</EmptyState>
    </>
  );
}
