import { EmptyState } from '../components/ui/States'
import { LinkButton } from '../components/ui/LinkButton'
import { ChamferFrame } from '../components/ui/hud'
import { PageHeader } from '../components/layout/PageHeader'

export default function NotFound() {
  return (
    <div>
      <PageHeader
        eyebrow="404"
        title="Page not found"
        description="That route does not exist in this console."
      />
      <div className="panel">
        <ChamferFrame />
        <EmptyState
          icon="alert"
          title="Nothing here"
          message="The address may be mistyped, or the page may not exist yet."
          action={<LinkButton to="/">Back to landing</LinkButton>}
        />
      </div>
    </div>
  )
}
