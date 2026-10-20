import en from './path-en.json'
import ru from './path-ru.json'
import de from './path-de.json'
import es from './path-es.json'
import fr from './path-fr.json'
import pt from './path-pt.json'
import zh from './path-zh.json'
import ja from './path-ja.json'
import {getLanguage} from '../i18n'
import type {Language} from '../languages'
export const pathContent:Record<Language,typeof en>={en,ru,de,es,fr,pt,zh,ja}
export function pathCopy(){return pathContent[getLanguage()]}
export function pathText(key:keyof typeof en.ui,values:Record<string,string|number>={}){
 return pathCopy().ui[key].replace(/\{(\w+)\}/g,(original,name:string)=>String(values[name]??original))
}
