import { useState } from 'react'
import { Download, FileImage, FileText, File, Loader2, ExternalLink } from 'lucide-react'
import { CollapsibleSection, SectionBadge } from './CollapsibleSection'
import { useJobArtifacts } from '@/hooks/useJobDetail'

interface ArtifactsPanelProps {
  jobId: string
  artifactCount: number
}

const TYPE_ICONS: Record<string, React.ElementType> = {
  original: File,
  original_tif: FileImage,
  registered_tif: FileImage,
  export_data: FileText,
  meta_json: FileText,
}

const TYPE_LABELS: Record<string, string> = {
  original: 'Original',
  original_tif: 'Original TIF',
  registered_tif: 'Registered TIF',
  export_data: 'Export data',
  meta_json: 'Metadata',
}

function formatBytes(b: number) {
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(0)} KB`
  return `${(b / (1024 * 1024)).toFixed(1)} MB`
}

export function ArtifactsPanel({ jobId, artifactCount }: ArtifactsPanelProps) {
  const [enabled, setEnabled] = useState(false)
  const { data: artifacts, isLoading } = useJobArtifacts(jobId, enabled)

  function handleOpen(open: boolean) {
    if (open) setEnabled(true)
  }

  return (
    <CollapsibleSection
      title="Artifacts"
      badge={<SectionBadge>{artifactCount}</SectionBadge>}
      onOpenChange={handleOpen}
    >
      {isLoading ? (
        <div className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" />
          Loading artifacts…
        </div>
      ) : !artifacts ? (
        <p className="text-xs text-muted-foreground py-1">Expand to load artifacts.</p>
      ) : artifacts.length === 0 ? (
        <p className="text-xs text-muted-foreground py-1">No artifacts yet.</p>
      ) : (
        <div className="space-y-1.5">
          {artifacts.map((a) => {
            const Icon = TYPE_ICONS[a.type] ?? File
            return (
              <div
                key={a.key}
                className="flex items-center gap-2 rounded border border-border/50 bg-muted/30 px-2.5 py-2 text-xs"
              >
                <Icon className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                <div className="min-w-0 flex-1">
                  <p className="font-medium">{TYPE_LABELS[a.type] ?? a.type}</p>
                  <p className="text-muted-foreground font-mono truncate text-[10px]">
                    {a.key.split('/').at(-1)}
                  </p>
                </div>
                <span className="text-muted-foreground tabular-nums shrink-0">
                  {formatBytes(a.size_bytes)}
                </span>
                {a.presigned_url ? (
                  <a
                    href={a.presigned_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    title="Download"
                    className="rounded p-1 hover:bg-accent transition-colors text-muted-foreground hover:text-foreground"
                  >
                    <ExternalLink className="h-3.5 w-3.5" />
                  </a>
                ) : (
                  <span className="rounded p-1 text-muted-foreground/40" title="No download available">
                    <Download className="h-3.5 w-3.5" />
                  </span>
                )}
              </div>
            )
          })}
        </div>
      )}
    </CollapsibleSection>
  )
}
