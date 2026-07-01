import { useState, useEffect } from "react";
import { AlertCircle, CheckCircle2, HardDrive } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Progress } from "@/components/ui/progress";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useStorageConfig, useStorageBackends, useMigrateStorage, useMigrationStatus } from "@/hooks/useStorageAPI";

export function StorageSettings() {
  const { t } = useTranslation("storage");
  const { config, loading: configLoading, refetch: refetchConfig } = useStorageConfig();
  const { backends, loading: backendsLoading } = useStorageBackends();
  const { migrate, loading: migratingLoading, error: migrateError } = useMigrateStorage();
  const [selectedBackend, setSelectedBackend] = useState<string>("");
  const [migrationId, setMigrationId] = useState<string | null>(null);
  const { status: migrationStatus } = useMigrationStatus(migrationId, (status) => {
    if (status?.status === "completed" || status?.status === "failed") {
      // Auto-refresh config after migration completes or fails
      setTimeout(() => refetchConfig(), 1000);
    }
  });

  useEffect(() => {
    if (config?.currentBackend && !selectedBackend) {
      setSelectedBackend(config.currentBackend);
    }
  }, [config, selectedBackend]);

  const handleMigrateClick = () => {
    if (!selectedBackend || selectedBackend === config?.currentBackend) return;

    migrate(selectedBackend, {
      onSuccess: (response) => {
        setMigrationId(response.migrationId);
      },
    });
  };

  const handleRetry = () => {
    setMigrationId(null);
    setSelectedBackend(config?.currentBackend ?? "");
  };

  const isMigrationInProgress = migrationStatus?.status === "pending" || migrationStatus?.status === "running";
  const isMigrationFailed = migrationStatus?.status === "failed";
  const isMigrationCompleted = migrationStatus?.status === "completed";
  const canMigrate =
    selectedBackend &&
    selectedBackend !== config?.currentBackend &&
    !migratingLoading &&
    !isMigrationInProgress &&
    !configLoading;

  const migrationProgress = migrationStatus?.progress
    ? (migrationStatus.progress.migrated / migrationStatus.progress.total) * 100
    : 0;

  return (
    <div className="space-y-6 max-w-3xl">
      {/* Current Backend Card */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <HardDrive className="h-5 w-5" />
            {t("storage.currentBackend")}
          </CardTitle>
          <CardDescription>{t("storage.backendInfo")}</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="space-y-4">
            <div>
              <div className="text-sm font-medium text-muted-foreground mb-2">
                {t("storage.currentBackendLabel")}
              </div>
              <div className="text-lg font-semibold">
                {configLoading ? "Loading..." : config?.currentBackend || "Not configured"}
              </div>
            </div>
            <div>
              <div className="text-sm font-medium text-muted-foreground mb-2">
                {t("storage.backendPath")}
              </div>
              <div className="text-sm text-foreground font-mono bg-muted p-2 rounded">
                {configLoading ? "Loading..." : config?.basePath || "—"}
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Storage Stats Card */}
      <Card>
        <CardHeader>
          <CardTitle>{t("storage.storageStats")}</CardTitle>
          <CardDescription>{t("storage.storageStatsDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="text-sm text-muted-foreground">
            {configLoading ? "Loading..." : t("storage.statsUnavailable")}
          </div>
        </CardContent>
      </Card>

      {/* Backend Selection & Migration Card */}
      <Card>
        <CardHeader>
          <CardTitle>{t("storage.selectBackend")}</CardTitle>
          <CardDescription>{t("storage.selectBackendDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <label htmlFor="backend-select" className="text-sm font-medium">
              {t("storage.availableBackends")}
            </label>
            <Select value={selectedBackend} onValueChange={setSelectedBackend} disabled={backendsLoading}>
              <SelectTrigger id="backend-select">
                <SelectValue placeholder={t("storage.selectBackendPlaceholder")} />
              </SelectTrigger>
              <SelectContent>
                {backends.map((backend) => (
                  <SelectItem key={backend.name} value={backend.name}>
                    {backend.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <Button
            onClick={handleMigrateClick}
            disabled={!canMigrate}
            size="lg"
            className="w-full h-12"
          >
            {migratingLoading ? t("storage.initiatingMigration") : t("storage.migrateButton")}
          </Button>

          {migrateError && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{migrateError}</AlertDescription>
            </Alert>
          )}
        </CardContent>
      </Card>

      {/* Migration Progress Card */}
      {migrationId && (
        <Card>
          <CardHeader>
            <CardTitle>{t("storage.migrationStatus")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-6">
            {/* Status Indicator */}
            <div className="flex items-center gap-3">
              {isMigrationInProgress && (
                <div className="h-3 w-3 bg-blue-500 rounded-full animate-pulse" />
              )}
              {isMigrationCompleted && (
                <CheckCircle2 className="h-5 w-5 text-green-500" />
              )}
              {isMigrationFailed && (
                <AlertCircle className="h-5 w-5 text-red-500" />
              )}
              <span className="font-medium">
                {isMigrationInProgress && t("storage.migrationInProgress")}
                {isMigrationCompleted && t("storage.migrationComplete")}
                {isMigrationFailed && t("storage.migrationFailed")}
              </span>
            </div>

            {/* Progress Bar */}
            {isMigrationInProgress && (
              <>
                <div>
                  <div className="flex justify-between items-center mb-2">
                    <span className="text-sm text-muted-foreground">
                      {t("storage.progressLabel")}
                    </span>
                    <span className="text-sm font-medium">
                      {migrationStatus?.progress.migrated ?? 0} / {migrationStatus?.progress.total ?? 0}
                    </span>
                  </div>
                  <Progress value={migrationProgress} className="h-2" />
                </div>
              </>
            )}

            {/* Error Message */}
            {isMigrationFailed && migrationStatus?.errorMessage && (
              <Alert variant="destructive">
                <AlertCircle className="h-4 w-4" />
                <AlertDescription>{migrationStatus.errorMessage}</AlertDescription>
              </Alert>
            )}

            {/* Retry Button */}
            {isMigrationFailed && (
              <Button onClick={handleRetry} variant="outline" className="w-full">
                {t("storage.retryMigration")}
              </Button>
            )}

            {/* Auto-hide after success */}
            {isMigrationCompleted && (
              <Alert className="border-green-200 bg-green-50 text-green-900">
                <CheckCircle2 className="h-4 w-4 text-green-600" />
                <AlertDescription className="text-green-800">
                  {t("storage.migrationCompleteMsg")}
                </AlertDescription>
              </Alert>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
