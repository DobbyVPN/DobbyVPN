package com.dobby.feature.authentication.domain

import dev.jordond.compass.Coordinates

object AirportsList {
    val airportsCoordinatesRU = listOf(
        Coordinates(59.9385, 30.2707), // SPbU MCS (for testing, remove this later)

        Coordinates(55.972500, 37.413056), // Sheremetyevo International Airport SVO
        Coordinates(55.408611, 37.906389), // Domodedovo International Airport DME
        Coordinates(59.800278, 30.262500), // Pulkovo Airport LED
        Coordinates(55.599167, 37.273056), // Vnukovo International Airport VKO
        Coordinates(52.267222, 104.394722), // Irkutsk International Airport IKT
        Coordinates(43.444444, 39.946944), // Sochi Airport AER
        Coordinates(55.033333, 82.599167), // Tolmachevo International Airport OBV
        Coordinates(43.398056, 132.148889), // Knevichi Airport VVO
        Coordinates(53.170278, 158.453611), // Yelizovo Airport PKC
        Coordinates(55.606944, 49.280278), // Kazan Airport KZN
        Coordinates(56.743056, 60.803056), // Koltsovo International Airport SVX
        Coordinates(48.528056, 135.188056), // Novy Airport KHV
        Coordinates(54.890000, 20.592500), // Khrabrovo Airport KGD
        Coordinates(47.493889, 39.924722), // Platov International Airport ROV
        Coordinates(54.557778, 55.873889), // Ufa Airport UFA
        Coordinates(55.552778, 38.149722), // Zhukovsky International Airport ZIA
        Coordinates(56.173056, 92.493333), // Yemelyanovo International Airport KJA
        Coordinates(59.911111, 150.720556), // Sokol GDX
        Coordinates(56.230000, 43.784167), // Strigino International Airport GOJ
        Coordinates(44.226803, 43.083014), // Mineralnyye Vody Airport MRV
        Coordinates(57.914444, 56.021389), // Bolshoye Savino International Airport PEE
        Coordinates(46.888611, 142.717500), // Khomutovo Airport UUS
        Coordinates(55.507500, 37.504167), // Ostafyevo International Airport OSF
        Coordinates(64.735000, 177.738333), // Ugolny Airport DYR
        Coordinates(57.168333, 65.316111), // Roschino International Airport TJM
        Coordinates(55.617222, 38.060000), // Bykovo Airport BKA
        Coordinates(55.305833, 61.503611), // Balandino Airport CEK
        Coordinates(54.967222, 73.310278), // Tsentralny Airport OMS
        Coordinates(51.807778, 107.438333), // Baikal International Airport UUD
        Coordinates(55.880833, 38.061667), // Chalovsky, Star City UUMU
        Coordinates(68.781667, 32.751111), // Murmansk Airport MMK
        Coordinates(55.819167, 37.432778), // Tushino UUUS
        Coordinates(48.782500, 44.345556), // Volgograd International Airport VOG
        Coordinates(42.817, 47.653), // Uytash Airport MCX
        Coordinates(55.564722, 52.092500), // Begishevo Airport NBC
        Coordinates(51.815000, 39.229722), // Chertovitskoye Airport VOZ
        Coordinates(47.699722, 40.282222), // Shakhty' Airport URRH
        Coordinates(43.388333, 45.699722), // Severny Airport GRV
        Coordinates(50.643889, 36.590000), // Belgorod International Airport EGO
        Coordinates(53.501111, 50.153889), // Kurumoch International Airport KUF
        Coordinates(45.034722, 39.170278), // Pashkovskiy Airport KRR
        Coordinates(60.014444, 29.702500), // By'ch'e Pole Airport ULLY
        Coordinates(56.942500, 40.932222), // Yuzhny Airport IWA
        Coordinates(66.070000, 76.519444), // Novy Urengoy Airport NUX
        Coordinates(47.198333, 38.849167), // Yuzhny Airport TGK
        Coordinates(59.980000, 30.585556), // Rzhevka RVH
        Coordinates(53.393056, 58.755556), // Magnitogorsk International Airport MQF
        Coordinates(51.750556, 36.295556), // Vostochny Airport URS
        Coordinates(64.378056, -173.243332), // Provideniya Bay Airport PVS
        Coordinates(54.998611, 55.751667), // Pervushino Airport UWUO
        Coordinates(54.440000, 53.388333), // Kyzyl Yar OKT
        Coordinates(54.548611, 36.371389), // Grabtsevo Airport KLF
        Coordinates(55.560833, 37.983889), // Myachkovo Airport UUBM
        Coordinates(62.093333, 129.773333), // Yakutsk Airport YKS
        Coordinates(45.002222, 37.347222), // Vityazevo Airport AAQ
        Coordinates(55.260556, 61.298333), // Shagol Airport USCG
        Coordinates(52.027, 113.305), // Kadala Airport HTA
        Coordinates(50.425556, 127.412500), // Ignatyevo Airport BQS
        Coordinates(53.214444, 34.175833), // Bryansk Airport BZK
        Coordinates(51.653333, 39.256944), // Pridacha Airport UUOD
        Coordinates(56.701944, 60.787500), // Aramil USSK
        Coordinates(64.600278, 40.716667), // Talagi Airport ARH
        Coordinates(59.684444, 30.336944), // Pushkin Airport ULLP
        Coordinates(57.560556, 40.157500), // Tunoshna Airport IAR
        Coordinates(53.363333, 83.539722), // Mikhaylovka Airport BAX
        Coordinates(57.783889, 28.395278), // Pskov Airport PKV
        Coordinates(56.833333, 53.456667), // Izhevsk Airport IJK
        Coordinates(44.581944, 38.013056), // Gelendzhik Airport GDZ
        Coordinates(54.641667, 52.800000), // Bugul'ma Airport UUA
        Coordinates(55.611389, 36.647778), // Kubinka Airport UUMB
        Coordinates(53.938056, 58.340000), // Beloreck BCX
        Coordinates(60.356111, 134.434722), // Ust'-Maya Airport UMS
        Coordinates(51.329167, 37.769167), // Stary'j Oskol Airport UUOS
        Coordinates(51.336389, 39.039444), // Borshhevo UUOY
        Coordinates(55.475000, 65.415278), // Kurgan Airport KRO
        Coordinates(56.106944, 54.347222), // NEF
        Coordinates(53.516389, 142.881111), // Novostroyka Airport OHH
        Coordinates(52.934722, 36.002222), // Uzhny Airport OEL
        Coordinates(57.796667, 41.018056), // Kostroma Airport KMW
        Coordinates(54.433333, 53.387500), // UVUK
        Coordinates(55.720278, 53.060000), // Menzelinsk Airport UWKP
        Coordinates(43.513, 43.637), // Nalchik Airport NAL
        Coordinates(56.218, 43.595), // UVGI
        Coordinates(51.795833, 55.456667), // Orenburg Tsentralny Airport REN
        Coordinates(53.154167, 140.651111), // Nikolaevsk-na-Amure Airport NLI
        Coordinates(50.409444, 136.934167), // Khurba KXK
        Coordinates(54.238889, 37.600278), // Klokovo TYA
        Coordinates(56.090278, 47.347222), // CSY
        Coordinates(57.727778, 60.130556), // Gran' Airport USSG
        Coordinates(53.689444, 127.091667), // Zeya Airport UHBE
        Coordinates(57.338611, 43.095000), // Yur'evecz Airport UUIC
        Coordinates(43.205278, 44.606667), // Beslan Airport OGZ
        Coordinates(55.866111, 49.132500), // Borisoglebskoye Airfield UWKG
        Coordinates(55.023333, 36.244167), // UUVK
        Coordinates(51.587222, 39.490278), // UUOQ
        Coordinates(57.730, 40.045), // Yaroslavl' Airport UUBX
        Coordinates(57.317778, 39.817222), // UUQA
        Coordinates(48.311944, 41.790278), // URRM
        Coordinates(59.242, 29.953), // ULSO
        Coordinates(59.488056, 29.988889), // ULSY
        Coordinates(59.726389, 29.640000), // ULSG
        Coordinates(51.565556, 46.045556), // Tsentralny Airport RTW
        Coordinates(59.884167, 30.169444), // ULSH
        Coordinates(66.050000, 117.399167), // UERE
        Coordinates(46.283333, 48.006389), // Narimanovo Airport ASF
        Coordinates(54.977, 37.657), // Novinki Airport UUDN
        Coordinates(56.310833, 40.481944), // UUIP
        Coordinates(56.401389, 39.958056), // Neby'loe Airport UUIN
        Coordinates(47.682778, 42.076667), // VLK
        Coordinates(56.181389, 40.329722), // UUIS
        Coordinates(54.631667, 37.366944), // Sonino Airport UUDC
        Coordinates(56.705000, 47.895278), // Yoshkar-Ola Airport JOK
        Coordinates(55.498, 36.208), // Mozhajskij Airport UUWH
        Coordinates(57.143333, 65.468056), // Plekhanovo Airport USTL
        Coordinates(52.703333, 39.537778), // Lipetsk Airport LPK
        Coordinates(55.228889, 36.608056), // UUWE
        Coordinates(53.717222, 52.371667), // Severny'j Airport UWWZ
        Coordinates(59.186111, 61.826111), // USSX
        Coordinates(56.127222, 40.316667), // UUBL
        Coordinates(56.128, 69.355), // USTM
        Coordinates(51.712778, 46.171111), // Gagarin International Airport GSV
        Coordinates(54.785556, 37.646667), // Bol'shoe Gry'zlovo Airport UUDG
        Coordinates(54.741667, 32.070000), // Smolensk South Airport UUBS
        Coordinates(54.358333, 41.987778), // UUBG
        Coordinates(54.625556, 37.580833), // Pakhomovo Airport UUDP
        Coordinates(54.399444, 48.801111), // Vostochny Airport ULY
        Coordinates(54.125278, 45.212778), // Saransk Airport SKX
        Coordinates(44.964444, 38.001389), // Krymska Airport NOI
        Coordinates(53.616667, 52.450000), // Glavny'j Airport UWWB
        Coordinates(52.805, 41.483), // Donskoe Airport TBW
        Coordinates(69.311111, 87.331944), // Alykel Airport NSK
        Coordinates(44.679444, 40.035833), // Khanskaya URKH
        Coordinates(44.652778, 40.091111), // URKM
        Coordinates(54.268333, 48.223611), // Baratayevka Airport ULV
        Coordinates(51.970000, 85.836944), // Gorno-Altaysk Airport RGK
        Coordinates(53.811944, 86.878333), // Spichenkovo Airport NOZ
        Coordinates(55.951667, 39.296667), // UUCB
        Coordinates(69.783, 170.595), // Pevek Airport PWE
        Coordinates(58.103611, 38.930000), // Staroselye Airport RYB
        Coordinates(58.396111, 45.553611), // Shar'ya Airport UUBQ
        Coordinates(51.850278, 107.736667), // Vostochny Airport UIUW
        Coordinates(52.688611, 58.715278), // UWUA
        Coordinates(43.323611, 45.012222), // Magas Airport IGT
        Coordinates(55.662500, 36.138333), // Vatulino Airport UUMV
        Coordinates(50.605278, 137.080000), // Dzemgi Airport UHKD
        Coordinates(55.305833, 50.615556), // Chistopol Airport UWKI
        Coordinates(58.625, 31.382), // ULLK
        Coordinates(45.109167, 42.112500), // Shpakovskoye Airport STW
        Coordinates(58.503611, 49.347222), // Pobedilovo Airport KVX
        Coordinates(66.451389, 143.260556), // Moma Airport MQJ
        Coordinates(55.066389, 37.883056), // UUCT
        Coordinates(56.383056, 85.210556), // Bogashevo Airport TOF
        Coordinates(59.281667, 39.944722), // Vologda Airport VGD
        Coordinates(61.647, 50.845), // Syktyvkar Airport SCW
        Coordinates(53.109167, 45.026111), // Penza Airport PEZ
        Coordinates(54.718333, 37.942500), // UUGI
        Coordinates(55.270000, 86.107500), // Kemerovo International Airport KEJ
        Coordinates(54.641389, 39.569444), // RZN
        Coordinates(45.249722, 39.079167), // Novotitarovskaya-Belevcy' Airport URKY
        Coordinates(53.740000, 91.385278), // Abakan Airport ABA
        Coordinates(46.374722, 44.323889), // Elista International Airport ESL
        Coordinates(54.586111, 31.565278), // Merlino Airport UUBJ
        Coordinates(60.788611, 46.260556), // Veliky Ustyug Airport VUS
        Coordinates(54.824444, 32.024167), // Smolensk Severny Aerodrome LNX
        Coordinates(52.479444, 85.338333), // Biysk Airport UNBI
        Coordinates(56.381111, 30.608056), // Velikiye Luki Airport VLU
        Coordinates(55.206111, 39.418333), // UUCQ
        Coordinates(59.276111, 38.018889), // Cherepovets Airport CEE
        Coordinates(54.904722, 39.027222), // Tretyakovo UUMT
        Coordinates(56.333333, 38.640000), // UUID
        Coordinates(61.885278, 34.154722), // Besovets Airport PES
        Coordinates(58.493611, 31.241667), // Novgorod Airport NVR
        Coordinates(56.797778, 37.329444), // Borki Airport UUEI
        Coordinates(68.151389, 33.464167), // Olenya AB XLMO
        Coordinates(50.058, 45.350), // Kamy'shin Airport URWK
        Coordinates(67.462778, 33.585556), // Khibiny Airport KVK
        Coordinates(67.647778, 134.693611), // Batagay Airport BQJ
        Coordinates(71.695556, 128.900556), // Tiksi Airport IKS
        Coordinates(59.992, 42.765), // ULWT
        Coordinates(51.072500, 58.595833), // Orsk Airport OSW
        Coordinates(43.948, 42.627), // URMK
        Coordinates(51.589167, 81.205278), // UNBR
        Coordinates(58.135833, 68.229167), // Tobol'sk Airport TOX
        Coordinates(60.948611, 76.480556), // Nizhnevartovsk Airport NJC
        Coordinates(55.205833, 38.679167), // Severka Airport UUML
        Coordinates(68.556944, 146.231111), // Belaya Gora Airport BGN
        Coordinates(55.129167, 39.146111), // Sel'nikovo Airport UUMD
        Coordinates(56.370556, 101.698611), // Bratsk Airport BTK
        Coordinates(54.019444, 48.314722), // Soldatskaya Tashla Airport UWLS
        Coordinates(59.580000, 56.858056), // Berezniki Airport USPT
        Coordinates(80.802, 47.668), // Nagurskoye Airport UODN
        Coordinates(51.565833, 46.282222), // Shumejka Airport UWSX
        Coordinates(43.960833, 145.685000), // Mendeleyevo Airport DEE
        Coordinates(50.960278, 46.945556), // Krasny'j Kut Airport UWSK
        Coordinates(63.020833, 179.290556), // UHMR
        Coordinates(60.013889, 37.761111), // Belozersk Airport ULWB
        Coordinates(44.060833, 42.830000), // URME
        Coordinates(63.746667, 159.922222), // UHGK
        Coordinates(62.534722, 114.038889), // Mirny Airport MJZ
        Coordinates(55.091667, 83.003889), // Yeltsovka Airport UNNE
        Coordinates(55.091667, 82.906111), // Severny UNCC
        Coordinates(46.544444, 43.645278), // Zavetnoye URRY
        Coordinates(54.928056, 38.811944), // UUCI
        Coordinates(56.023333, 38.326944), // UUMC
        Coordinates(51.571111, 85.928611), // UNCX
        Coordinates(47.258333, 39.818056), // Rostov-on-Don Airport URRR
        Coordinates(55.169444, 83.144722), // UNNM
        Coordinates(56.143611, 34.991944), // UUTO
        Coordinates(53.417, 109.017), // UIUI
        Coordinates(45.841667, 137.678333), // AEM
        Coordinates(51.344444, 110.533333), // Khilok Airport UICH
        Coordinates(79.528, 91.075), // UODS
        Coordinates(51.858333, 47.745556), // BWO
        Coordinates(66.358, -179.107), // UHME
        Coordinates(71.978333, 102.493333), // Khatanga Airport HTG
        Coordinates(56.913889, 35.935000), // Zmeyevo Airport UUBN
        Coordinates(49.043333, 134.313333), // Pobeda Airport UHHJ
        Coordinates(55.455833, 89.174444), // UNKO
        Coordinates(45.878333, 133.736111), // DLR
        Coordinates(60.301111, 60.074722), // USSE
        Coordinates(62.789444, 136.855000), // Teply'j Klyuch Airport KDY
        Coordinates(50.623056, 107.522222), // Bichura Airport UIUA
        Coordinates(64.955278, 36.822778), // Letnyaya Zoloticza Airport ULBZ
        Coordinates(60.107222, 64.825833), // Uray Airport URJ
        Coordinates(56.913889, 124.913889), // Chulman Neryungri Airport NER
        Coordinates(67.350, 37.067), // Krasnoshhel'e Airport ULMX
        Coordinates(61.235833, 46.697500), // KSZ
        Coordinates(45.073333, 136.592500), // Terney Airport NEI
        Coordinates(53.586944, 109.709444), // UIAO
        Coordinates(53.696667, 49.491667), // Verkhnee Sancheleevo Airport UWWE
        Coordinates(52.533, 111.550), // UIUS
        Coordinates(42.920833, 133.903056), // Preobrazheniye Airport RZH
        Coordinates(56.824736, 35.757678), // Migalovo KLD
        Coordinates(61.343889, 73.402222), // Surgut Airport SGC
        Coordinates(45.256389, 147.955833), // Iturup Airport ITU
        Coordinates(44.276389, 135.036944), // KVR
        Coordinates(63.909722, 38.123056), // ULAO
        Coordinates(57.961111, 31.384444), // Staraya Russa Airport ULNR
        Coordinates(65.029722, 35.733611), // Solovki Airport CSH
        Coordinates(56.900278, 74.288333), // Tara Airport UNOT
        Coordinates(56.268333, 90.570833), // ACS
        Coordinates(63.887222, 122.775833), // UENK
        Coordinates(66.697778, 34.399444), // ULMA
        Coordinates(60.556389, 169.104722), // Pakhachi Airport UHPA
        Coordinates(66.400556, 112.030278), // Polyarny Airport PYJ
        Coordinates(52.367778, 104.183333), // Vostochny Airport UIIR
        Coordinates(60.858611, 135.307778), // UEQK
        Coordinates(47.596667, 138.368333), // Agzu Airport UHTZ
        Coordinates(66.377778, 42.557778), // Kojda Airport ULBJ
        Coordinates(52.751944, 41.731111), // Zarya Airport OO20
        Coordinates(52.033, 106.583), // UIUX
        Coordinates(49.215833, 133.462778), // Kukan Airport UHHW
        Coordinates(49.586111, 111.956944), // Ky'ra Airport UIAK
        Coordinates(52.167778, 109.796111), // UIUH
        Coordinates(71.928056, 114.079444), // SYS
        Coordinates(66.055556, 43.426111), // Dolgoshhel'e Airport ULBD
        Coordinates(58.313333, 112.889722), // Mama Airport UIKM
        Coordinates(70.010833, 135.643333), // Ust'-Kujga Airport UKG
        Coordinates(64.441667, 40.422222), // Vas'kovo Airport ULAH
        Coordinates(56.635000, 36.887500), // UUEL
        Coordinates(50.669167, 142.760833), // Zonalnoye UHSO
        Coordinates(47.180000, 138.648333), // Edinka Airport EDN
        Coordinates(63.458889, 120.268611), // Verkhnevilyujsk Airport VHV
        Coordinates(67.792222, 130.392500), // Sakkyryr Airport SUK
        Coordinates(44.558611, 135.490000), // Dalnegorsk Airport DHG
        Coordinates(51.324444, 108.890278), // Petrovsk-Zabajkal'skij Airport UICP
        Coordinates(48.925278, 140.036111), // Maygatka UHKM
        Coordinates(58.585278, 76.504167), // UNLW
        Coordinates(53.385556, 92.044722), // UNAU
        Coordinates(56.513611, 34.971944), // UUTS
        Coordinates(54.316389, 112.352500), // Varvarinsk Airport UIUR
        Coordinates(54.300, 110.333), // UIUK
        Coordinates(64.385556, 37.440556), // Purnema Airport ULBM
        Coordinates(61.921944, 159.229722), // Severo-Evensk Airport SWV
        Coordinates(56.778611, 36.280833), // Volzhanka Airport UUEY
        Coordinates(73.516667, 80.381667), // Dikson Airport DKS
        Coordinates(56.176667, 92.543333), // Cheremshanka Airport UNKM
        Coordinates(58.473889, 92.113056), // Yeniseysk Airport EIE
        Coordinates(53.656111, 111.942500), // UIUV
        Coordinates(66.590556, 66.611111), // Salehkard Airport SLY
        Coordinates(54.725000, 20.573333), // Kaliningrad Devau Airport
        Coordinates(64.790000, 38.423333), // Pertominsk Airport ULAT
        Coordinates(65.783056, 43.383056), // ULBQ
        Coordinates(61.703056, 53.693611), // UUYT
        Coordinates(69.763333, 61.556389), // AMV
        Coordinates(50.370833, 108.764444), // Krasny'j Chikoj Airport UIAD
        Coordinates(54.230, 96.967), // UNYG
        Coordinates(66.117, 38.833), // ULMP
        Coordinates(64.982778, 37.695833), // Lopshen'ga Airport ULBL
        Coordinates(66.117, 37.733), // ULMQ
        Coordinates(44.920000, 147.621944), // Burevestnik AFB BVV
        Coordinates(44.815, 136.292), // Plastun Airport TLY
        Coordinates(61.324444, 63.604444), // Tyumenskaya Airport OVS
        Coordinates(53.617, 98.252), // UINY
        Coordinates(47.247778, 138.790000), // UHTS
        Coordinates(61.676667, 96.353333), // UNIB
        Coordinates(64.512778, 121.083611), // UENT
        Coordinates(55.178056, 166.048333), // Nikolskoye Airport UHPX
        Coordinates(60.373056, 93.011111), // UNIS
        Coordinates(52.486667, 96.086667), // UNYT
        Coordinates(68.676667, 151.872500), // UESX
        Coordinates(54.888, 99.067), // UINN
        Coordinates(59.082222, 159.891111), // Palana Airport UHPL
        Coordinates(53.238, 50.378), // UVVS
        Coordinates(56.858056, 105.729444), // Ust-Kut Airport UKX
        Coordinates(59.622222, 150.921667), // Magadan-13 Airport UHMT
        Coordinates(66.031389, 41.211944), // Ruch'i Airport ULBR
        Coordinates(49.183611, 142.084722), // EKS
        Coordinates(46.541111, 138.318333), // ETL
        Coordinates(65.686667, 40.360000), // Verkhnyaya Zoloticza Airport ULBW
        Coordinates(60.385278, 166.025556), // Tilichiki Airport UHPT
        Coordinates(68.701667, 134.422778), // UEBY
        Coordinates(64.549722, 143.113056), // USR
        Coordinates(51.783889, 143.141667), // Nogliki Airport NGK
        Coordinates(57.451667, 132.527500), // Mar-Kyuel' Airport UHNK
        Coordinates(56.238889, 162.690000), // Ust'-Kamchatsk Airport UHPK
        Coordinates(58.380556, 97.472778), // Boguchany' Airport UNKB
        Coordinates(52.756667, 97.420000), // Khamsara Airport UNYA
        Coordinates(54.418889, 119.941111), // Tupik Airport UICT
        Coordinates(46.093333, 137.893333), // UHTM
        Coordinates(64.165, 171.053), // UHAW
        Coordinates(70.624722, 147.896389), // Chokurdakh Airport CKH
        Coordinates(71.214444, 72.038889), // Sabeta International Airport SBT
        Coordinates(68.372500, 143.620278), // UEMN
        Coordinates(57.100000, 156.737500), // Ust'-Khajryuzovo Airport UHPU
        Coordinates(63.757222, 121.691667), // Vilyuisk Airport VYI
        Coordinates(51.669444, 94.400556), // Kyzyl Airport KYZ
        Coordinates(59.148333, 68.917778), // Uvat Airport USTA
        Coordinates(70.808333, 133.505556), // UEBG
        Coordinates(55.283333, 124.777222), // Tynda Airport TYD
        Coordinates(57.615278, 79.456944), // UNCK
        Coordinates(54.369444, 113.479444), // UIUB
        Coordinates(53.473333, 125.795000), // GDG
        Coordinates(52.441667, 136.483333), // UHHP
        Coordinates(62.108056, 129.564167), // Magan Airport GYG
        Coordinates(57.783611, 158.728056), // Tigil' Airport UHPG
        Coordinates(58.583889, 76.502500), // UNLV
        Coordinates(64.495000, 46.146667), // ULJO
        Coordinates(63.566944, 53.804444), // Ukhta Airport UCT
        Coordinates(57.864444, 114.238333), // Bodaybo Airport ODO
        Coordinates(61.028611, 69.086111), // Khanty-Mansiysk Airport HMA
        Coordinates(47.179444, 39.426389), // Dugino Airport RR07
        Coordinates(59.223611, 163.066389), // Ossora Airport UHPD
        Coordinates(59.401667, 143.051667), // OHO
        Coordinates(64.618056, 30.687222), // Kostomuksha Airport ULPM
        Coordinates(52.716667, 95.775000), // UNYY
        Coordinates(51.138056, 132.936111), // Chegdomy'n Airport UHHM
        Coordinates(68.516111, 112.480000), // Olenek Airport ONK
        Coordinates(49.413, 130.068), // UHBA
        Coordinates(66.595, 44.663), // ULJN
        Coordinates(67.474722, 153.729444), // Srednekoly'msk Airport SEK
        Coordinates(68.392222, 150.721667), // UESQ
        Coordinates(68.868333, -179.375555), // UHMI
        Coordinates(58.603333, 125.407778), // Aldan Airport ADH
        Coordinates(61.591667, 89.999722), // Podkamennaya Tunguska Airport TGP
        Coordinates(64.665278, 170.416944), // KVM
        Coordinates(51.456389, 128.097222), // Svobodny Airport UHBS
        Coordinates(67.125, 39.718), // ULMZ
        Coordinates(57.106944, 60.619444), // Im. Kuzneczova Airport SS60
        Coordinates(67.640278, 53.121944), // Naryan-Mar Airport NNM
        Coordinates(51.493889, 98.061667), // Severny'j Arzhan Airport UNYS
        Coordinates(51.483333, 95.566667), // Sary'g-Sep Airport UNYE
        Coordinates(54.681389, 158.550278), // Mil'kovo Airport UHPM
        Coordinates(48.521667, 135.155000), // UHHT
        Coordinates(64.550556, 122.266944), // UEYS
        Coordinates(56.361667, 114.927222), // UIKG
        Coordinates(54.685278, 135.283333), // UHHY
        Coordinates(63.474722, 48.840278), // UUYL
        Coordinates(52.380278, 140.448889), // Bogorodskoe Airport BQG
        Coordinates(64.895833, 45.721111), // Leshukonskoye Airport LDG
        Coordinates(54.306944, 155.974167), // Sobolevo Airport UHPS
        Coordinates(54.918333, 64.471944), // USUK
        Coordinates(66.651667, 46.511667), // ULJM
        Coordinates(58.136111, 102.565278), // UIK
        Coordinates(65.878056, 44.113333), // Kamenka Airport ULBK
        Coordinates(69.392500, 139.889444), // Deputatskij Airport DPT
        Coordinates(60.400833, 120.471111), // Olekminsk Airport OLZ
        Coordinates(52.659444, 115.245000), // UIAY
        Coordinates(56.090000, 159.876667), // Kozyrevsk Airport UHPO
        Coordinates(50.935833, 138.187778), // Nizhnetambovskoe Airport UHKN
        Coordinates(66.795278, 123.362500), // Zhigansk Airport ZIX
        Coordinates(52.853, 156.347), // UHPB
        Coordinates(69.396944, 88.353611), // Valek Airport UOOW
        Coordinates(53.743611, 119.741944), // Mogocha Airport UIAM
        Coordinates(51.546667, 118.369167), // Gazimurskij Zavod Airport UIAS
        Coordinates(58.480000, 99.095278), // UNKI
        Coordinates(52.245556, 117.734722), // Sretensk Airport UICS
        Coordinates(68.741389, 161.339722), // Cherskij Airport CYX
        Coordinates(48.520556, 134.931389), // Priamurskaya Airport UHHQ
        Coordinates(59.055278, 119.780000), // UEOQ
        Coordinates(59.650, 67.433), // USHK
        Coordinates(65.878333, 44.216944), // ULAE
        Coordinates(59.183611, 131.884722), // UEEX
        Coordinates(63.296944, 118.343056), // Nyurba Airport NYR
        Coordinates(50.031944, 118.061389), // Krasnokamensk Airport UIAE
        Coordinates(56.279444, 107.567222), // Kazachinsk Airport UITK
        Coordinates(53.026667, 158.720000), // UHPH
        Coordinates(62.731944, 56.196111), // UUYR
        Coordinates(55.479444, 113.643333), // Uakit Airport UIUT
        Coordinates(56.914167, 118.270000), // Chara Airport UIAR
        Coordinates(47.322778, 39.449167), // Shhedry'j Airport RR05
        Coordinates(61.835278, 160.548611), // Chajbukha Airport UHMG
        Coordinates(67.846944, 166.135278), // Keperveem Airport KPW
        Coordinates(70.315278, 68.333611), // Bovanenkovo Airport BVJ
        Coordinates(63.370833, 125.543889), // UEMD
        Coordinates(69.373889, 86.155278), // Dudinka Airport UROD
        Coordinates(48.955278, 40.297222), // URRI
        Coordinates(51.499167, 156.543611), // Ozernaya Airport UHQO
        Coordinates(65.748333, 150.888333), // Zyryanka Airport ZKP
        Coordinates(67.437500, 86.621944), // Igarka Airport IAA
        Coordinates(54.375833, 61.353056), // Uprun USCU
        Coordinates(57.770556, 108.059167), // Kirensk Airport KCK
        Coordinates(65.031389, 53.971111), // Izhma Airport UUYV
        Coordinates(62.110556, 65.613889), // Nyagan Airport NYA
        Coordinates(55.801111, 109.593889), // UIUN
        Coordinates(63.962222, 118.644722), // UEDN
        Coordinates(58.325278, 82.932500), // UNLL
        Coordinates(57.355000, 139.497222), // UHOI
        Coordinates(56.893611, 124.868611), // Chulman Airport UELD
        Coordinates(54.952778, 61.502222), // Chelavia Airport CC03
        Coordinates(64.536667, 48.476667), // Vozhgora Airport ULJW
        Coordinates(65.958889, 111.546667), // UERA
        Coordinates(49.444722, 136.570833), // UHHO
        Coordinates(64.358889, 120.489722), // UEYY
        Coordinates(64.622778, 120.190278), // UEYL
        Coordinates(64.733333, 47.665000), // Kojnas Airport ULJK
        Coordinates(56.430, 138.048), // UHNA
        Coordinates(53.077500, 132.964444), // UHBP
        Coordinates(47.246389, 38.840278), // Taganrog AB URRC
        Coordinates(68.466944, 73.597222), // My's Kamenny'j Airport YMK
        Coordinates(66.901667, 169.560000), // UHED
        Coordinates(62.186111, 117.634722), // Suntar Airport SUY
        Coordinates(65.580556, -170.998610), // Lavrentiya Airport UHML
        Coordinates(65.774167, 46.202778), // Moseevo Airport ULAJ
        Coordinates(60.720000, 114.823333), // Lensk Airport ULK
        Coordinates(59.881667, 111.045556), // Talakan Airport TLK
        Coordinates(46.678611, 38.210833), // URKE
        Coordinates(52.975, 138.817), // UHNH
        Coordinates(65.800000, 87.928333), // THX
        Coordinates(57.058333, 40.979444), // Ivanovo Severnyy AB UUDI
        Coordinates(70.154167, 113.980833), // Dzhelinda Airport UERC
        Coordinates(66.055556, 60.110833), // Inta Airport INA
        Coordinates(59.149444, 76.279722), // Pionerny'j Airport UNSP
        Coordinates(61.390556, 73.205000), // Poligon Skol Airport USRG
        Coordinates(64.391111, 122.212222), // UERY
        Coordinates(53.158889, 34.696944), // Frolovka Airport BC20
        Coordinates(53.519444, 59.200278), // Primorskij Airport CM01
        Coordinates(69.255, 64.952), // ULDU
        Coordinates(63.959167, 127.421111), // Sangar Airport UEMS
        Coordinates(67.489167, 63.989444), // Vorkuta Airport VKT
        Coordinates(71.437778, 136.192500), // UEBN
        Coordinates(54.642222, 39.569444), // Dyagilevo AB UUBD
        Coordinates(70.726389, 139.236667), // UESR
        Coordinates(45.680833, 38.821389), // Berkut Airport KK20
        Coordinates(65.482222, 72.698333), // Nadym Airport NYM
        Coordinates(64.888333, 46.706667), // Cenogora Airport ULJC
        Coordinates(64.832500, 120.941944), // UEND
        Coordinates(66.553333, 132.961111), // UEVS
        Coordinates(50.600, 97.533), // UNYK
        Coordinates(65.537, 168.850), // UHAL
        Coordinates(65.172, 167.953), // UHAC
        Coordinates(62.158572, 77.328903), // Vareghan West RAT
        Coordinates(68.057500, 151.796667), // UESV
        Coordinates(55.149167, 34.383056), // GC0022
        Coordinates(67.765556, 144.509444), // UEMG
        Coordinates(59.458, 112.563), // UERT
        Coordinates(53.581389, 52.580000), // Zav'yalovka Airport WB05
        Coordinates(63.183889, 75.268889), // Noyabrsk Airport NOJ
        Coordinates(62.485833, 165.336389), // Manily' Airport UHPN
        Coordinates(62.190556, 74.533611), // Kogalym International Airport KGP
        Coordinates(60.708889, 77.661111), // Strezhevoy Airport SWT
        Coordinates(68.443056, 153.362500), // UESH
        Coordinates(67.120000, 156.589722), // UESC
        Coordinates(62.919444, 152.422222), // UHMS
        Coordinates(52.640833, 39.450833), // Lipetsk AB GC0018
        Coordinates(57.641667, 136.161667), // UHNX
        Coordinates(66.740, 47.022), // ULJS
        Coordinates(64.334, 100.438), // UNIT
        Coordinates(53.522222, 55.780278), // UWUS
        Coordinates(65.001111, 153.096389), // Glukharinoe Airport UHGL
        Coordinates(61.243611, 73.323056), // USRB
        Coordinates(45.087778, 37.748611), // Adagum Airport KK30
        Coordinates(62.766667, 148.146389), // Susuman Airport UHMH
        Coordinates(65.238333, 160.541944), // Omolon Airport UHMN
        Coordinates(45.555833, 39.224722), // Dyad'kovskaya Airport KK14
        Coordinates(68.284444, 154.717222), // UESD
        Coordinates(61.105556, 80.249167), // USNL
        Coordinates(57.729167, 134.441389), // UHNU
        Coordinates(46.836111, 40.380556), // URRG
        Coordinates(43.623889, 43.579444), // Kishpek Airport UR02
        Coordinates(65.371389, 143.168056), // UEAT
        Coordinates(66.738333, 47.731667), // ULAV
        Coordinates(65.437778, 52.200000), // Ust'-Cil'ma Airport UTS
        Coordinates(60.254444, 90.195833), // Yarcevo Airport UNIQ
        Coordinates(62.458333, 155.745556), // UHMF
        Coordinates(68.848333, 58.201111), // Varandey Airport VRI
        Coordinates(51.170556, 128.443056), // Ukrainka AB UHBU
        Coordinates(55.016667, 135.404722), // UHNI
        Coordinates(45.104722, 38.113611), // Troiczkaya Airport KK24
        Coordinates(60.356389, 102.310556), // Vanavara Airport UNIW
        Coordinates(61.107778, 72.648889), // NFG
        Coordinates(46.539167, 39.547778), // URRD
        Coordinates(62.567, 149.550), // UHGE
        Coordinates(51.531667, 43.301389), // UWSW
        Coordinates(43.298056, 45.784167), // Khankala, Grozny East GC0027
        Coordinates(69.190, 50.245), // ULEP
        Coordinates(61.275, 108.032), // Erbogachen Airport ERG
        Coordinates(65.679444, 47.673056), // Safonovo Airport ULAK
        Coordinates(65.121389, 57.130833), // Pechora Airport PEX
        Coordinates(64.930556, 77.811111), // Tarko-Sale Airport TQL
        Coordinates(59.067222, 80.819444), // UNLK
        Coordinates(68.076944, 87.644722), // Snezhnogorsk Airport UOIC
        Coordinates(60.198611, 30.335556), // Agalatovo ULLN
        Coordinates(48.636111, 43.789444), // URWM
        Coordinates(52.297222, 43.724167), // UWPR
        Coordinates(53.715000, 33.339444), // UUWD
        Coordinates(63.824167, 57.279444), // UUYK
        Coordinates(63.688056, 66.699722), // Beloyarskiy Airport EYK
        Coordinates(52.073611, 113.432778), // Chita-2 UIAI
        Coordinates(66.839722, 88.403889), // Svetlogorsk Airport UOIG
        Coordinates(66.004167, 57.369167), // Usinsk Airport USK
        Coordinates(52.917500, 40.365556), // UUWM
        Coordinates(55.441389, 42.311389), // UUDE
        Coordinates(67.443, 58.048), // ULEH
        Coordinates(52.915000, 103.575000), // Belaya AB UIIB
        Coordinates(47.634444, 43.095278), // URRK
        Coordinates(63.986944, 82.051389), // Tol'ka Airport USDO
        Coordinates(67.027222, 51.113889), // ULEK
        Coordinates(67.988333, 75.096944), // Yamburg Airport USMQ
        Coordinates(63.197778, 64.441944), // Igrim Airport IRM
        Coordinates(65.715833, 82.456111), // Krasnosel'kup Airport KKQ
        Coordinates(51.215000, 47.008056), // Pushkino Airport SS65
        Coordinates(60.714444, 30.112222), // Gromovo AB ULLJ
        Coordinates(66.850, 59.533), // ULER
        Coordinates(57.000000, 29.816111), // Bezhanicy' Airport OL01
        Coordinates(54.340833, 32.472500), // UUBV
        Coordinates(44.258, 43.240), // Aleksandrijskaya Airport MP01
        Coordinates(50.931944, 47.190000), // Yamskoe Airport SS66
        Coordinates(50.753056, 47.025556), // Komsomol'skoe Airport SS63
        Coordinates(45.880278, 40.105278), // URKT
        Coordinates(44.964444, 38.001389), // URKW
        Coordinates(63.921389, 65.030000), // Berezovo Airport EZV
        Coordinates(64.275833, 100.216389), // Tura-Mvl Airport UNHT
        Coordinates(67.326944, 52.085278), // ULEL
        Coordinates(54.493333, 39.931111), // Protasovo Oblastnoy Aeroport UUWP
        Coordinates(67.484167, 78.644444), // USDT
        Coordinates(48.308889, 46.229444), // URWH
        Coordinates(55.458611, 39.035833), // Spetspriyemnik UUMG
        Coordinates(52.034444, 48.817222), // UWSP
        Coordinates(65.960278, 78.436667), // Urengoj Airport USDU
        Coordinates(44.944722, 38.936389), // Enem URKS
        Coordinates(43.906944, 131.924722), // Vozdvizhenka AB GC0048
        Coordinates(56.260000, 34.408333), // Rzhev-3 UUER
        Coordinates(43.787500, 44.603056), // URMF
        Coordinates(44.968611, 41.107778), // URKR
        Coordinates(53.225556, 48.550000), // UWWS
        Coordinates(66.891389, 33.878889), // Khibini GC0023
        Coordinates(45.448333, 39.417778), // URKO
        Coordinates(59.190278, 39.123889), // Fedotovo AB ULWF
        Coordinates(43.348333, 132.059167), // Tsentralnaya Uglovaya UHIU
        Coordinates(67.575556, 33.582778), // GC0049
        Coordinates(56.309722, 160.804167), // Klyuchi-20 UHPW
        Coordinates(49.235833, 140.194167), // Kamenny Ruchey Naval Air Base UHKG
        Coordinates(69.030000, 33.423889), // Severomorsk-1 ULMD
        Coordinates(64.252778, 60.921944), // Saranpaul' Airport USHA
        Coordinates(43.907500, 131.925000), // Vozdvizhenka AB UHWV
        Coordinates(44.322222, 132.545000), // UHII
        Coordinates(47.751667, 135.640556), // Bichevaya Airport UH73
        Coordinates(56.365278, 36.735556), // UUMN
        Coordinates(53.109167, 50.098889), // Kryazh UWWV
        Coordinates(51.264167, 128.720000), // Orlovka AB
        Coordinates(50.446944, 127.868056), // Serko Airport UH84
        Coordinates(53.533333, 49.580833), // Vasil'evka Airport WW58
        Coordinates(44.261667, 133.411667), // Varfolomeyevka AB UHIF
        Coordinates(55.760000, 93.770278), // UNQM
        Coordinates(67.988889, 33.019167), // Monchegorsk AB ULMG
        Coordinates(44.829167, 44.011667), // URMB
        Coordinates(68.866944, 33.718056), // Severomorsk-3 ULMV
        Coordinates(48.462500, 135.150833), // Tsentral'nyy Aerodrome UHHA
        Coordinates(53.349722, 158.181111), // Koryaki Airport PP42
        Coordinates(58.435833, 33.888889), // Borovichi Airport ULNB
        Coordinates(56.123889, 95.662222), // UNKN
        Coordinates(57.299722, 28.433889), // ULOS
        Coordinates(45.083889, 38.944444), // Krasnodar AB URKL
        Coordinates(61.538056, 129.169167), // UEEP
        Coordinates(62.422778, 60.845000), // Nyaksimvol' Airport HB10
        Coordinates(48.620000, 135.119722), // Khokhlaczkaya-1 Airport UH03
        Coordinates(57.044444, 35.002222), // UUET
        Coordinates(63.199722, 59.802222), // Pripolyarny'j Airport
        Coordinates(62.724722, 64.361389), // Svetly'j Airport
    )
}