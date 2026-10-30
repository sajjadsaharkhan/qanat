import { Activity } from "lucide-react";
import { EmptyState, PageHeader } from "@/components/qanat/page";

export const metadata = { title: "Recent requests" };

export default function Page() {
  return (
    <>
      <PageHeader title="Recent requests" icon={Activity} />
      <EmptyState title="No requests yet">Your last 200 requests show here with model, time to first token, tokens, cost and status. Prompts are never shown.</EmptyState>
    </>
  );
}
