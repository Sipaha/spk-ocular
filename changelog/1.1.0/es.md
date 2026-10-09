# SPK Ocular 1.1.0

- Compilación con Go 1.26.9 y golang.org/x/net v0.60.0 para corregir las vulnerabilidades HTTP/2 recién publicadas.
- Explore archivos de contenedores Kubernetes y Docker: árbol de carpetas en caché, destinos de enlaces simbólicos, resaltado de sintaxis y edición protegida de UTF-8 hasta 2 MiB. La aplicación de escritorio copia archivos o carpetas completos mediante un selector nativo de directorio, sin sobrescribir nombres existentes.
- Abra los registros en una ventana nativa independiente manteniendo un único flujo. Elija dónde guardar las exportaciones de texto. Se muestran todos los niveles; se conservan los filtros de texto y la búsqueda.
- Elija un Pod en workloads con varias instancias y un contenedor en Pods con varios contenedores. Clic derecho en Logs, Terminal o Files abre la configuración completa; solo Terminal incluye un campo de comando. Una opción única se abre directamente con clic izquierdo y sigue visible en la configuración.
- Consulte las revisiones conservadas de Deployment y compare YAML de la plantilla del Pod, de solo lectura, con el Deployment actual u otra revisión.
- Los atajos siguen las teclas físicas independientemente de la distribución, también en el editor. Se corrigen la repetición de caracteres cirílicos, los cambios intermedios de tamaño del terminal y el foco que permanecía en el separador.
- Más espacio en el inspector, árbol redimensionable, recuentos en caché y filas estables de carga/carpetas vacías. Los errores no desplazan el diseño. Los recursos relacionados usan filas compactas y las acciones de ventana aparecen como iconos a la derecha.

Las operaciones de archivos requieren un contenedor Linux en ejecución con sh, readlink para enlaces y tar para descargas. Las descargas nativas no tienen el límite de 2 MiB del editor y requieren la aplicación de escritorio. Se rechazan entradas inseguras del archivo y colisiones de nombres.

Paquetes nativos para Linux, Windows y macOS en amd64 y arm64. No cambia el estado de firma/notarización ni se reescriben archivos de versiones anteriores.
