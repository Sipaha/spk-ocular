import {getLanguage} from '../i18n'
import type {DownloadRecord} from './store'

export function downloadSize(bytes:number) {
 const units=['byte','kilobyte','megabyte','gigabyte','terabyte'] as const
 const index=bytes>0?Math.min(4,Math.floor(Math.log(bytes)/Math.log(1000))):0
 if(index===0){const unit=new Intl.NumberFormat(getLanguage(),{style:'unit',unit:'byte',unitDisplay:'narrow'}).formatToParts(bytes).find(part=>part.type==='unit')?.value??'B';return new Intl.NumberFormat(getLanguage()).format(bytes)+' '+unit}
 return new Intl.NumberFormat(getLanguage(),{style:'unit',unit:units[index],unitDisplay:'short',maximumFractionDigits:index===0?0:1}).format(bytes/1000**index)
}

export function downloadTime(timestamp:number,now:number) {
 const seconds=Math.max(0,Math.floor((now-timestamp)/1000))
 if(seconds<60)return ''
 const [size,unit]:[number,Intl.RelativeTimeFormatUnit]=seconds<3600?[60,'minute']:seconds<86400?[3600,'hour']:seconds<604800?[86400,'day']:seconds<2592000?[604800,'week']:seconds<31536000?[2592000,'month']:[31536000,'year']
 return new Intl.RelativeTimeFormat(getLanguage(),{numeric:'always'}).format(-Math.floor(seconds/size),unit)
}

export function downloadType(record:DownloadRecord) {
 if(record.directory)return 'folder'
 if(record.kind==='logs')return 'text'
 const name=record.name.toLowerCase()
 if(/\.(zip|tar|gz|bz2|xz|7z|rar|tgz|zst)$/.test(name))return 'archive'
 if(/\.(png|jpe?g|gif|webp|svg|bmp|ico|avif)$/.test(name))return 'image'
 if(/\.(json|ya?ml|xml|toml|ini|js|ts|tsx|jsx|py|go|sh|css|html|sql)$/.test(name))return 'code'
 if(/\.(log|txt|md|csv)$/.test(name))return 'text'
 return 'file'
}
