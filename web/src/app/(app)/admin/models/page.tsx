import { Shapes } from "lucide-react";
import { EmptyState, PageHeader } from "@/components/qanat/page";

export const metadata = { title: "Models" };

export default function Page() {
  return (
    <>
      <PageHeader title="Models" icon={Shapes} />
      <EmptyState title="No model mapping yet">The global model mapping, Combos and the price table arrive with the gateway.</EmptyState>
    </>
  );
}
