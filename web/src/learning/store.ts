import {create} from 'zustand'
interface LearningState { request:{kind?:string}|null; open:(kind?:string)=>void; close:()=>void }
export const useLearning=create<LearningState>(set=>({request:null,open:kind=>set({request:{kind}}),close:()=>set({request:null})}))
