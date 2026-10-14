# SPK Ocular 1.6.1

- Los paquetes Linux DEB/RPM requieren glibc 2.35 o posterior en lugar de 2.39. Ubuntu 22.04 y Debian 12 pueden instalarlos normalmente si disponen de GTK 3 y WebKit2GTK 4.1.
- Ambas arquitecturas Linux pasan comprobaciones nativas de instalación, modo navegador y ventana GTK en Ubuntu 22.04 antes de la publicación. El empaquetado también rechaza ejecutables con símbolos glibc superiores al mínimo declarado.
- El archivo de verificación nativa añade dos informes y capturas de Ubuntu 22.04 a las pruebas de las seis plataformas existentes. Son comprobaciones de X11 y del espacio de usuario con el kernel del entorno de pruebas; no certifican todos los kernels, controladores GPU ni sesiones Wayland.
- Las confirmaciones de configuración enfocan correctamente Cancelar y mantienen los eventos del teclado dentro del diálogo. Enter en Cancelar cierra la confirmación sin eliminar las configuraciones cifradas.
- En el menú Add kubeconfig, Enter y Espacio activan la opción enfocada sin ejecutar los atajos de la lista de destinos. Mantener Enter pulsado y las opciones deshabilitadas siguen sin activar acciones.
