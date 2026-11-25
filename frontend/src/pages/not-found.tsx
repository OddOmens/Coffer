import { FileQuestionIcon } from "lucide-react"
import { Link } from "react-router"

import { Brand } from "@/components/brand"
import { EmptyState } from "@/components/states"
import { Button } from "@/components/ui/button"

export default function NotFoundPage({ publicOnly }: { publicOnly?: boolean }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-8 p-6">
      <Brand />
      <EmptyState
        icon={<FileQuestionIcon />}
        title={publicOnly ? "Nothing here" : "Page not found"}
        description={publicOnly ? "Open the full link you were sent to view shared files." : "The page you asked for does not exist."}
      >
        {!publicOnly && (
          <Button asChild variant="outline">
            <Link to="/files">Go to Files</Link>
          </Button>
        )}
      </EmptyState>
    </div>
  )
}
