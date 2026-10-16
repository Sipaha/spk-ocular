# SPK Ocular 1.7.1

- Se corrigieron el desplazamiento de toda la página y el espacio vacío bajo la barra de estado al reducir la ventana con un panel de registros grande. El panel se ajusta ahora a la altura disponible y mantiene la cabecera y el pie de la aplicación en su sitio.
- Las compilaciones locales con Make incluyen ahora la hora de compilación UTC con precisión de segundos y una revisión corta de Git en la versión del pie, el diálogo Acerca de y el comando version. Los números de las versiones publicadas no cambian.
- Se corrigió una condición de carrera en la cancelación de Helm upgrade que podía marcar como fallida una actualización ya completada con éxito durante la limpieza de la operación. Se mantienen la cancelación activa y los límites de tiempo.
