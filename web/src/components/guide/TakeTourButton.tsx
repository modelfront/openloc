// Starts a saved tour outside the chat (empty lists), in the
// browser, without the assistant. With `playbook` it starts that tour;
// without, it opens a menu of every tour the role can take, plus the way into
// the demo organization. Outside the demo a tour runs there, on sample data
// (useStartSavedTour). Renders nothing when there is no tour to offer.
import { ChevronDown, Compass } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useStartSavedTour } from '@/lib/playbooks'

export function TakeTourButton({
  playbook,
  label,
  className,
}: {
  playbook?: string
  label?: string
  className?: string
}) {
  const { tours, inDemo, start, openDemo } = useStartSavedTour()

  if (playbook) {
    const pb = tours.find(p => p.name === playbook)
    if (!pb) return null
    return (
      <Button
        variant="outline"
        size="sm"
        className={className}
        onClick={() => start(pb.name)}
        data-testid={`take-tour-${playbook}`}
        title={pb.description}
      >
        <Compass className="h-4 w-4" />
        {label ?? 'Take the tour'}
      </Button>
    )
  }

  if (tours.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button variant="outline" size="sm" className={className} data-testid="take-tour-menu">
            <Compass className="h-4 w-4" />
            <span className="flex-1 text-left">{label ?? 'Take a tour'}</span>
            <ChevronDown className="h-3.5 w-3.5 opacity-60" />
          </Button>
        }
      />
      <DropdownMenuContent align="start" className="w-80">
        {tours.map(pb => (
          <DropdownMenuItem
            key={pb.name}
            data-testid={`take-tour-${pb.name}`}
            description={pb.description}
            onClick={() => start(pb.name)}
          >
            {pb.title}
          </DropdownMenuItem>
        ))}
        {!inDemo && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              data-testid="take-tour-demo"
              description="Tours run there, on sample data. Nothing you do there is saved."
              onClick={openDemo}
            >
              Explore the Playground
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
