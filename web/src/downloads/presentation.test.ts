import {afterEach,describe,expect,it} from 'vitest'
import {loadLanguage,setLanguage} from '../i18n'
import {languages} from '../languages'
import {downloadSize,downloadTime,downloadType} from './presentation'
import type {DownloadRecord} from './store'
afterEach(()=>setLanguage('en'))
const record:DownloadRecord={id:'file',name:'unknown',kind:'files',status:'complete',bytes:0,startedAt:0}
describe('download presentation',()=>{
 it('formats readable decimal units and clamps future timestamps',()=>{
  setLanguage('en');expect(downloadSize(0)).toBe('0 B');expect(downloadSize(1500)).toBe('1.5 kB');expect(downloadSize(1536000)).toBe('1.5 MB');expect(downloadSize(2500000000)).toBe('2.5 GB')
  expect(downloadTime(1000,4000)).toBe('');expect(downloadTime(10000,1000)).toBe('');expect(downloadTime(0,59999)).toBe('');expect(downloadTime(0,60000)).toBe('1 minute ago');expect(downloadTime(0,120000)).toBe('2 minutes ago')
 })
 it.each(languages)('localizes sizes and relative dates in %s',async lang=>{await loadLanguage(lang);setLanguage(lang);expect(downloadSize(1536000)).toBeTruthy();expect(downloadTime(1000,4000)).toBe('');expect(downloadTime(1000,121000)).toBe(new Intl.RelativeTimeFormat(lang,{numeric:'always'}).format(-2,'minute'))})
 it('distinguishes native directories, archives, images, configurations and plain logs',()=>{
  expect(downloadType({...record,directory:true,name:'folder.zip'})).toBe('folder')
  expect(downloadType({...record,name:'BACKUP.TAR.GZ'})).toBe('archive');expect(downloadType({...record,name:'photo.png'})).toBe('image');expect(downloadType({...record,name:'settings.yaml'})).toBe('code');expect(downloadType({...record,kind:'logs'})).toBe('text');expect(downloadType(record)).toBe('file')
 })
})
