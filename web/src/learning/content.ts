import en from './en.json'
import ru from './ru.json'
import zh from './zh.json'
import es from './es.json'
import de from './de.json'
import fr from './fr.json'
import pt from './pt.json'
import ja from './ja.json'
import {getLanguage} from '../i18n'
import type {Language} from '../languages'
import {concept, type ConceptId} from './catalog'

// Shipped with the application: opening help never fetches content or schemas.
export const learningContent: Record<Language, typeof en> = {en,ru,zh,es,de,fr,pt,ja}
export type LearningKey = keyof typeof en.ui
export function learn(key:LearningKey) { return learningContent[getLanguage()].ui[key] }
export function explanation(id:ConceptId) {
 const entry=concept(id)!
 const local=learningContent[getLanguage()]
 return {purpose:local.purposes[id], caution:local.cautions[entry[2]]}
}
