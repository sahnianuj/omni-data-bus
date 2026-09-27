import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './en.json'

// English only in 1.0; every user-visible string lives in en.json (OC-004 AC8).
void i18n.use(initReactI18next).init({
  resources: { en: { translation: en } },
  lng: 'en',
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
  returnNull: false,
})

export default i18n
