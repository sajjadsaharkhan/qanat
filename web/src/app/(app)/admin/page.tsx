import { EmptyState, PageHeader } from "@/components/qanat/page";

export const metadata = { title: "Needs you" };

/** design.md §5: the ranked attention queue. Empty means all is quiet. */
export default function NeedsYouPage() {
  return (
    <>
      <PageHeader title="Needs you" />
      <EmptyState title="Nothing needs you">
        Disconnected accounts, unhealthy proxies, low pool headroom and users near their quota will queue here, most urgent first.
      </EmptyState>
    </>
  );
}
