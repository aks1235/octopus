import { Fragment, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Database, Download, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { toast } from 'sonner';
import { useExportDB, useImportDB, type DBImportConflict } from '@/api/setting';

const CONFLICT_OPTIONS: DBImportConflict[] = ['skip', 'overwrite', 'rename'];

export function SettingBackup() {
    const t = useTranslations('setting');

    const exportDB = useExportDB();
    const importDB = useImportDB();

    const [file, setFile] = useState<File | null>(null);
    const [conflict, setConflict] = useState<DBImportConflict>('skip');
    const fileInputRef = useRef<HTMLInputElement | null>(null);

    const rowsAffected = importDB.data?.rows_affected ?? null;
    const skipped = importDB.data?.skipped ?? null;
    const resultRows = useMemo(() => {
        if (!rowsAffected && !skipped) return [];
        const tables = new Set([...Object.keys(rowsAffected ?? {}), ...Object.keys(skipped ?? {})]);
        return [...tables]
            .sort((a, b) => a.localeCompare(b))
            .map((table) => ({
                table,
                written: rowsAffected?.[table] ?? 0,
                skipped: skipped?.[table] ?? 0,
            }));
    }, [rowsAffected, skipped]);

    const onPickFile = (f: File | null) => {
        setFile(f);
    };

    const onImport = async () => {
        if (!file) {
            toast.error(t('backup.import.noFile'));
            return;
        }
        try {
            await importDB.mutateAsync({ file, conflict });
            toast.success(t('backup.import.success'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.import.failed'));
        }
    };

    const onExport = async () => {
        try {
            await exportDB.mutateAsync();
            toast.success(t('backup.export.success'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.export.failed'));
        }
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Database className="h-5 w-5" />
                {t('backup.title')}
            </h2>

            {/* 导出 */}
            <div>
                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={onExport}
                    disabled={exportDB.isPending}
                >
                    <Download className="size-4" />
                    {exportDB.isPending ? t('backup.export.exporting') : t('backup.export.button')}
                </Button>
            </div>

            <div className="h-px bg-border" />

            {/* 导入 */}
            <div className="space-y-3">
                <div className="text-sm font-semibold text-card-foreground">{t('backup.import.title')}</div>

                {/* 同名对象处理策略: 目标是另一个已配置的实例时, 按主键合并会串配置, 故按名称判定并让用户选语义 */}
                <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">{t('backup.import.conflict.label')}</div>
                    <Select value={conflict} onValueChange={(v) => setConflict(v as DBImportConflict)}>
                        <SelectTrigger className="w-full rounded-xl">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="rounded-xl">
                            {CONFLICT_OPTIONS.map((option) => (
                                <SelectItem key={option} value={option} className="rounded-xl">
                                    {t(`backup.import.conflict.${option}`)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <div className="text-xs text-muted-foreground">
                        {t(`backup.import.conflict.hint.${conflict}`)}
                    </div>
                </div>

                <Input
                    ref={fileInputRef}
                    type="file"
                    accept="application/json,.json"
                    onChange={(e) => onPickFile(e.target.files?.[0] ?? null)}
                    className="rounded-xl"
                />

                <Button
                    type="button"
                    variant="destructive"
                    className="w-full rounded-xl"
                    onClick={onImport}
                    disabled={importDB.isPending}
                >
                    <Upload className="size-4" />
                    {importDB.isPending ? t('backup.import.importing') : t('backup.import.button')}
                </Button>

                {resultRows.length > 0 && (
                    <div className="mt-2 space-y-1">
                        <div className="text-xs font-semibold text-card-foreground">{t('backup.import.result')}</div>
                        <div className="grid grid-cols-3 gap-x-2 gap-y-1 text-xs text-muted-foreground">
                            <span />
                            <span className="text-right">{t('backup.import.written')}</span>
                            <span className="text-right">{t('backup.import.skipped')}</span>
                            {resultRows.map((it) => (
                                <Fragment key={it.table}>
                                    <span className="truncate">{it.table}</span>
                                    <span className="text-right tabular-nums">{it.written}</span>
                                    <span className="text-right tabular-nums">{it.skipped}</span>
                                </Fragment>
                            ))}
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
}
