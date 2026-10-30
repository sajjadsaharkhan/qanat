import { EmptyState, PageHeader } from "@/components/qanat/page";

export const metadata = { title: "Accounts" };

export default function Page() {
  return (
    <>
      <PageHeader title="Accounts" />
      <EmptyState title="No provider accounts connected">Connecting Claude subscription accounts arrives in milestone M5.</EmptyState>
    </>
  );
}
