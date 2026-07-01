import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { HardDrive, Folder } from "lucide-react";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { PageHeader } from "@/components/shared/page-header";
import { StorageFileBrowser } from "./storage-file-browser";
import { StorageSettings } from "./storage-settings";

export function StoragePage() {
  const { t } = useTranslation("storage");
  const [params, setParams] = useSearchParams();

  const tab = params.get("tab") ?? "files";

  const setTab = (v: string) => {
    const next = new URLSearchParams(params);
    next.set("tab", v);
    setParams(next, { replace: true });
  };

  return (
    <div className="flex flex-col h-full p-4 sm:p-6">
      <PageHeader
        title={t("title")}
        description={t("description")}
      />

      <div className="mt-6 flex-1 flex flex-col min-h-0">
        <Tabs value={tab} onValueChange={setTab} className="flex flex-col flex-1 min-h-0">
          <TabsList>
            <TabsTrigger value="files" className="flex items-center gap-2">
              <Folder className="h-4 w-4" />
              {t("tabs.files")}
            </TabsTrigger>
            <TabsTrigger value="configuration" className="flex items-center gap-2">
              <HardDrive className="h-4 w-4" />
              {t("tabs.configuration")}
            </TabsTrigger>
          </TabsList>

          <TabsContent value="files" className="mt-4 flex-1 flex flex-col min-h-0">
            <StorageFileBrowser />
          </TabsContent>

          <TabsContent value="configuration" className="mt-4 flex-1 overflow-y-auto">
            <StorageSettings />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
