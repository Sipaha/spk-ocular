import type { Client } from '../api/client'
import type { Ref } from '../api/types'
import type { TermOpen } from '../dock/store'
import { ToolDialog } from '../components/ToolDialog'

export function TerminalDialog({client, subject, onOpen, onClose}: {client:Client;subject:Ref;onOpen:(open:TermOpen)=>void;onClose:()=>void}) {
  return <ToolDialog client={client} subject={subject} mode="terminal" onClose={onClose}
    onOpen={selection=>onOpen({ref:selection.ref,instance:selection.instance?.id,channel:selection.channel,command:selection.command})}/>
}
